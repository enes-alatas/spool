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
// MISSION with the conflict line, and disabling it removes the section on
// the loop's next wake without a restart.
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

	// a change lands on the next wake: sleep the loop, disable, wake again
	s.waitState("ruled", "asleep", 30*time.Second)
	s.mustJSON("PATCH", "/api/rules/"+rule.Rule.ID, map[string]any{"enabled": false}, nil)
	patchedAt := time.Now().UnixMilli()
	s.message("ruled", "and now")
	// the creation tick and the first message both replayed the prompt, so
	// pick the turn by time rather than by id
	second := s.waitTurn("ruled", 15*time.Second, func(tn turn) bool {
		return tn.EndedAt >= patchedAt && strings.Contains(tn.ResultText, "MISSION")
	})
	if strings.Contains(second.ResultText, "FLEET RULES") {
		t.Fatalf("disabled rule still in the prompt:\n%s", second.ResultText)
	}
}
