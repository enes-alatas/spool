//go:build integration

package itest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fleetRule struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Enabled bool   `json:"enabled"`
}

type rulesBudget struct {
	SectionChars    int `json:"section_chars"`
	SectionCharsMax int `json:"section_chars_max"`
	TitleCharsMax   int `json:"title_chars_max"`
	BodyCharsMax    int `json:"body_chars_max"`
}

type rulesView struct {
	Rules  []fleetRule `json:"rules"`
	Budget rulesBudget `json:"budget"`
}

type ruleWriteView struct {
	Rule   fleetRule   `json:"rule"`
	Budget rulesBudget `json:"budget"`
}

func (s *server) rules() rulesView {
	s.t.Helper()
	var v rulesView
	s.mustJSON("GET", "/api/rules", nil, &v)
	return v
}

func (s *server) createRule(title, body string, enabled bool) ruleWriteView {
	s.t.Helper()
	var v ruleWriteView
	s.mustJSON("POST", "/api/rules", map[string]any{"title": title, "body": body, "enabled": enabled}, &v)
	return v
}

// TestFleetRulesCRUD drives the rules resource end to end: the list carries
// the budget the guard measures, writes echo it, per-field caps and the
// section cap reject server-side with a machine-readable code, a shrinking
// edit is never refused, and DELETE answers 204.
func TestFleetRulesCRUD(t *testing.T) {
	s := startServer(t, t.TempDir())

	fresh := s.rules()
	if len(fresh.Rules) != 0 || fresh.Budget.SectionChars != 0 {
		t.Fatalf("fresh server: %+v, want no rules and an empty section", fresh)
	}
	if fresh.Budget.SectionCharsMax != 4000 || fresh.Budget.TitleCharsMax != 120 || fresh.Budget.BodyCharsMax != 1200 {
		t.Fatalf("budget caps = %+v, want 4000/120/1200", fresh.Budget)
	}

	created := s.createRule("sign your work", "End every artifact with your name.", true)
	if !strings.HasPrefix(created.Rule.ID, "rule_") || !created.Rule.Enabled {
		t.Fatalf("created = %+v", created.Rule)
	}
	if created.Budget.SectionChars == 0 {
		t.Fatal("write did not echo the moved budget")
	}
	if got := s.rules(); len(got.Rules) != 1 || got.Budget.SectionChars != created.Budget.SectionChars {
		t.Fatalf("list after create = %+v, want the echoed budget", got)
	}

	// per-field validation: empty and over-cap text is rejected without storing
	for _, bad := range []map[string]any{
		{"title": "", "body": "b"},
		{"title": "t", "body": ""},
		{"title": strings.Repeat("x", 121), "body": "b"},
		{"title": "t", "body": strings.Repeat("x", 1201)},
	} {
		if resp, body := s.do("POST", "/api/rules", bad); resp.StatusCode != 400 {
			t.Fatalf("POST %v: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	if got := s.rules(); len(got.Rules) != 1 {
		t.Fatalf("a rejected rule was stored: %+v", got.Rules)
	}

	// the section cap: three max-size bodies fit, a fourth pushes the rendered
	// section past 4000 and is refused with the budget it would have produced
	for i := 0; i < 3; i++ {
		s.createRule("filler", strings.Repeat("x", 1200), true)
	}
	resp, body := s.do("POST", "/api/rules", map[string]any{"title": "one too many", "body": strings.Repeat("x", 1200), "enabled": true})
	if resp.StatusCode != 400 {
		t.Fatalf("over-cap create: status %d, want 400 (%s)", resp.StatusCode, body)
	}
	var rejection struct {
		Code   string      `json:"code"`
		Budget rulesBudget `json:"budget"`
	}
	if err := json.Unmarshal(body, &rejection); err != nil || rejection.Code != "rules_too_large" {
		t.Fatalf("rejection = %s, want code rules_too_large", body)
	}
	if rejection.Budget.SectionChars <= 4000 {
		t.Fatalf("rejection budget = %+v, want the proposed size over the cap", rejection.Budget)
	}

	// the same rule stores disabled, and enabling it is what the guard refuses
	dormant := s.createRule("one too many", strings.Repeat("x", 1200), false)
	if resp, body := s.do("PATCH", "/api/rules/"+dormant.Rule.ID, map[string]any{"enabled": true}); resp.StatusCode != 400 ||
		!strings.Contains(string(body), "rules_too_large") {
		t.Fatalf("enable past the cap: status %d (%s), want 400 rules_too_large", resp.StatusCode, body)
	}
	if got := s.rules(); got.Rules[len(got.Rules)-1].Enabled {
		t.Fatal("a refused enable was stored")
	}
	// shorter, it enables
	var enabled ruleWriteView
	s.mustJSON("PATCH", "/api/rules/"+dormant.Rule.ID, map[string]any{"enabled": true, "body": "short"}, &enabled)
	if !enabled.Rule.Enabled || enabled.Rule.Body != "short" || enabled.Budget.SectionChars > 4000 {
		t.Fatalf("shortened enable = %+v", enabled)
	}

	if resp, _ := s.do("PATCH", "/api/rules/rule_missing", map[string]any{"title": "x"}); resp.StatusCode != 404 {
		t.Fatalf("PATCH unknown: status %d, want 404", resp.StatusCode)
	}
	if resp, _ := s.do("DELETE", "/api/rules/"+created.Rule.ID, nil); resp.StatusCode != 204 {
		t.Fatalf("DELETE: status %d, want 204", resp.StatusCode)
	}
	if resp, _ := s.do("DELETE", "/api/rules/"+created.Rule.ID, nil); resp.StatusCode != 404 {
		t.Fatalf("DELETE again: status %d, want 404", resp.StatusCode)
	}
	if got := s.rules(); len(got.Rules) != 4 {
		t.Fatalf("after delete: %d rules, want 4", len(got.Rules))
	}
}

// TestFleetRulesReachThePrompt pins the injection (ADR-0024): an enabled
// rule shows up in a loop's system prompt as a FLEET RULES section ahead of
// MISSION with the conflict line, and a change to the rules catches up with
// a running loop without a restart.
//
// "Without a restart" is not "on the next wake", because `claude --resume`
// keeps the system prompt its session was created with and ignores a changed
// --append-system-prompt (#162). The next wake's prompt is still the old one
// — asserted here, since a fake that quietly honoured the new one is what
// made this bug survive a green test for weeks — and what binds the loop
// meanwhile is the note
// (TestStandingInstructionsReachARunningSession). The system prompt itself
// is replaced by the loop's next rotation, asked for here so the row does
// not wait on one the loop would have had for its own reasons.
func TestFleetRulesReachThePrompt(t *testing.T) {
	s := startServer(t, t.TempDir())
	rule := s.createRule("sign your work", "End every artifact with your name.", true)

	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("ruled", map[string]any{"workspace_path": ws, "workspace_mode": "dir", "mission": "keep the tests green"})

	s.message("ruled", "show me")
	first := s.waitTurn("ruled", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "MISSION")
	})
	section := "FLEET RULES\n1. sign your work\n   End every artifact with your name.\nWhere a fleet rule and your mission conflict, the fleet rule wins.\n\nMISSION\nkeep the tests green"
	if !strings.Contains(first.ResultText, section) {
		t.Fatalf("system prompt lacks the rules section ahead of the mission:\n%s", first.ResultText)
	}

	// sleep the loop, disable the rule, wake it again
	s.waitState("ruled", "asleep", 30*time.Second)
	s.mustJSON("PATCH", "/api/rules/"+rule.Rule.ID, map[string]any{"enabled": false}, nil)
	patchedAt := time.Now().UnixMilli()
	s.message("ruled", "and now")
	// the creation tick and the first message both replayed the prompt, so
	// pick the turn by time rather than by id
	second := s.waitTurn("ruled", 15*time.Second, func(tn turn) bool {
		return tn.EndedAt >= patchedAt && strings.Contains(tn.ResultText, "MISSION")
	})
	if second.SessionID != first.SessionID {
		t.Fatalf("the loop was expected to resume its session, not rotate yet: %s -> %s", first.SessionID, second.SessionID)
	}
	if !strings.Contains(second.ResultText, "FLEET RULES") {
		t.Fatalf("a resumed session was expected to still run the prompt it was created with:\n%s", second.ResultText)
	}

	// A fresh session is the only kind that can be given a new prompt, so
	// the rule is gone from the one the rotation starts.
	s.mustJSON("POST", "/api/loops/ruled/rotate", nil, nil)
	rotatedAt := time.Now().UnixMilli()
	s.message("ruled", "after the rotation")
	third := s.waitTurn("ruled", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= rotatedAt && tn.SessionID != first.SessionID &&
			strings.Contains(tn.ResultText, "MISSION")
	})
	if strings.Contains(third.ResultText, "FLEET RULES") {
		t.Fatalf("disabled rule still in the rotated session's prompt:\n%s", third.ResultText)
	}
}

// TestStandingInstructionsReachARunningSession is the immediate half of the
// same promise: a session that cannot be given a new system prompt is told in
// the only place it can still be reached, ahead of the work that wake brought
// it. The note carries every section that changes outside a release — the
// loop's own mission as well as the fleet's rules and catalog — so an edit
// to any of them is delivered and not merely announced.
func TestStandingInstructionsReachARunningSession(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!echo\n")
	s.createLoop("bound", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})

	s.message("bound", "first")
	s.waitTurn("bound", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "first")
	})
	s.waitState("bound", "asleep", 30*time.Second)

	// One edit per section the note carries, so none of the three can be
	// announced without being delivered. A burst costs one note, not one
	// each, because the comparison is against the prompt as a whole.
	s.createRule("no telemetry", "Never phone home.", true)
	s.createLoop("witness", map[string]any{"mission": "watch what bound does"})
	s.mustJSON("PATCH", "/api/loops/bound", map[string]any{"mission": "hold the line"}, nil)
	changedAt := time.Now().UnixMilli()

	s.message("bound", "second")
	delta := s.waitTurn("bound", 15*time.Second, func(tn turn) bool {
		return tn.EndedAt >= changedAt && strings.Contains(tn.ResultText, "second")
	})
	// The prefix, not a substring: the note leads the turn, ahead of the
	// wake's own envelopes, so the loop reads what now binds it before it
	// reads the work it binds.
	const opener = "echo: [system note · your standing instructions changed]"
	if !strings.HasPrefix(delta.ResultText, opener) {
		t.Fatalf("the turn the loop received does not open with the note:\n%s", delta.ResultText)
	}
	for _, want := range []string{
		"FLEET RULES\n1. no telemetry\n   Never phone home.", // the new rule
		"MISSION\nhold the line",                             // the edited mission
		"@witness — watch what bound does",                   // the new peer
		"second",                                             // and the wake's own envelope, after all of it
	} {
		if !strings.Contains(delta.ResultText, want) {
			t.Fatalf("the turn the loop received lacks %q:\n%s", want, delta.ResultText)
		}
	}

	// And it is said once. The session is still the one that was told, so
	// this is the real check: a later wake compares against the prompt the
	// note paid for, not the one the session was created with.
	s.waitState("bound", "asleep", 30*time.Second)
	at := time.Now().UnixMilli()
	s.message("bound", "third")
	again := s.waitTurn("bound", 15*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "third")
	})
	if again.SessionID != delta.SessionID {
		t.Fatalf("nothing should have rotated the loop: %s -> %s", delta.SessionID, again.SessionID)
	}
	if strings.HasPrefix(again.ResultText, opener) {
		t.Fatalf("the note was repeated on a wake with nothing new to say:\n%s", again.ResultText)
	}
}

// TestStandingInstructionsAreNotSpentOnAHandoffTurn: the one turn decision 3
// does not reach is a rotation's handoff turn (ADR-0024, amendment
// 2026-09-18). That session is ending and its reply is a note to its
// successor; standing instructions it can no longer act on would only crowd
// that out. The successor is bound by the stronger mechanism — it is spawned
// with the prompt rendered at that wake (TestFleetRulesReachThePrompt) — so
// it is not told either, and must not be: a note announcing a change to a
// session whose own system prompt already contains it is noise.
func TestStandingInstructionsAreNotSpentOnAHandoffTurn(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!echo\n")
	s.createLoop("rotator", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})

	s.message("rotator", "first")
	first := s.waitTurn("rotator", 15*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "first")
	})
	s.waitState("rotator", "asleep", 30*time.Second)

	// A rule the loop now owes itself a note about, and a rotation before
	// the wake that would deliver it.
	s.createRule("no telemetry", "Never phone home.", true)
	s.mustJSON("POST", "/api/loops/rotator/rotate", nil, nil)
	at := time.Now().UnixMilli()
	s.message("rotator", "after the rotation")

	const note = "standing instructions changed"
	handoff := s.waitTurn("rotator", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && tn.Trigger == "rotation"
	})
	if strings.Contains(handoff.ResultText, note) {
		t.Fatalf("the note was spent on the handoff turn, which cannot act on it:\n%s", handoff.ResultText)
	}

	successor := s.waitTurn("rotator", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "after the rotation")
	})
	if successor.SessionID == first.SessionID {
		t.Fatalf("the loop was expected to rotate onto a fresh session, still on %s", first.SessionID)
	}
	if strings.Contains(successor.ResultText, note) {
		t.Fatalf("a fresh session was told its own system prompt changed:\n%s", successor.ResultText)
	}
}
