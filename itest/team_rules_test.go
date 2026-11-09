//go:build integration

package itest

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// hubSection is the FLEET RULES section a loop in no team is shown for the
// one hub rule these tests make, followed by the mission it sits ahead of:
// the same bytes as before rules had a scope.
const hubSection = "FLEET RULES\n1. sign your work\n   End every artifact with your name.\n" +
	"Where a fleet rule and your mission conflict, the fleet rule wins.\n\nMISSION\n"

// A fleet rule can be scoped to one team (ADR-0066, #1436). That
// team's loops see it after the hub's rules, with one line naming it as
// their team's and saying a hub rule wins over it. No other loop sees it,
// and a loop in no team, or in a team with no rules, is shown the section
// byte for byte as before.
func TestATeamsRuleReachesItsLoopsAlone(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, team := range []string{"red", "blue"} {
		hub.mustJSON("POST", "/api/teams", map[string]any{"name": team}, nil)
	}
	hub.createRule("sign your work", "End every artifact with your name.", true)
	var red ruleWriteView
	hub.mustJSON("POST", "/api/rules", map[string]any{"title": "red reviews first", "body": "Ask red-review before you merge.", "team": "red"}, &red)
	if red.Rule.Team != "red" {
		t.Fatalf("the team's rule answers as %+v, want team red", red.Rule)
	}

	for name, team := range map[string]string{"crimson": "red", "azure": "blue", "loner": ""} {
		hub.createLoop(name, map[string]any{"workspace_path": workspaceWithScript(t, "!sysprompt\n"), "workspace_mode": "dir",
			"mission": "keep the tests green", "team": team})
	}
	prompt := func(name string) string {
		hub.message(name, "show me")
		return hub.waitTurn(name, firstTurnWait, func(tn turn) bool { return strings.Contains(tn.ResultText, "MISSION") }).ResultText
	}

	want := "FLEET RULES\n1. sign your work\n   End every artifact with your name.\n" +
		"2. red reviews first\n   Ask red-review before you merge.\n" +
		"Where a fleet rule and your mission conflict, the fleet rule wins.\n" +
		"Rule 2 is team red's; where it conflicts with a hub rule, the hub rule wins.\n\nMISSION\n"
	if got := prompt("crimson"); !strings.Contains(got, want) {
		t.Errorf("team red's loop was shown:\n%s\nwant the hub's rule, then red's, then the line:\n%s", got, want)
	}
	for _, name := range []string{"azure", "loner"} {
		got := prompt(name)
		if !strings.Contains(got, hubSection) || strings.Contains(got, "red reviews first") || strings.Contains(got, "team red") {
			t.Errorf("%s was shown:\n%s\nwant the hub's section alone, as before:\n%s", name, got, hubSection)
		}
	}
}

// The section cap counts what one loop is shown (ADR-0066): a hub
// rule that would push a team's section over it is refused naming that
// team, though a loop in no team would have room. Deleting a team deletes
// its rules, and no loop's prompt carries them after.
func TestTeamRulesKeepToTheCapAndGoWithTheirTeam(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red"}, nil)
	hub.createRule("sign your work", "End every artifact with your name.", true)
	long := strings.Repeat("x", 1200)
	for i := 0; i < 3; i++ {
		hub.mustJSON("POST", "/api/rules", map[string]any{"title": "red", "body": long, "team": "red"}, nil)
	}

	resp, raw := hub.do("POST", "/api/rules", map[string]any{"title": "hub", "body": long})
	status, body := resp.StatusCode, string(raw)
	if status != http.StatusBadRequest || !strings.Contains(body, `"code":"rules_too_large"`) || !strings.Contains(body, "team red's loops") {
		t.Fatalf("a hub rule over team red's cap answered %d %s, want 400 rules_too_large naming team red", status, body)
	}
	resp, raw = hub.do("POST", "/api/rules", map[string]any{"title": "red", "body": "x", "team": "blue"})
	status, body = resp.StatusCode, string(raw)
	if status != http.StatusNotFound || !strings.Contains(body, "team_not_found") {
		t.Fatalf("a rule for a team the hub doesn't have answered %d %s, want 404 team_not_found", status, body)
	}

	hub.createLoop("crimson", map[string]any{"workspace_path": workspaceWithScript(t, "!sysprompt\n"), "workspace_mode": "dir",
		"mission": "keep the tests green", "team": "red"})
	hub.message("crimson", "show me")
	first := hub.waitTurn("crimson", firstTurnWait, func(tn turn) bool { return strings.Contains(tn.ResultText, "MISSION") })
	if !strings.Contains(first.ResultText, "team red's") {
		t.Fatalf("team red's loop was shown no team rules:\n%s", first.ResultText)
	}

	hub.mustJSON("PATCH", "/api/loops/crimson", map[string]any{"team": ""}, nil)
	if resp, raw := hub.do("DELETE", "/api/teams/red", nil); resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting team red answered %d %s", resp.StatusCode, raw)
	}
	for _, rule := range hub.rules().Rules {
		if rule.Team != "" {
			t.Errorf("after the team's delete a rule of team %q is left: %+v", rule.Team, rule)
		}
	}
	if got := hub.rules().Budget.Teams; len(got) != 0 {
		t.Errorf("after the team's delete the budget still measures %+v", got)
	}

	hub.mustJSON("POST", "/api/loops/crimson/rotate", nil, nil)
	rotatedAt := time.Now().UnixMilli()
	hub.message("crimson", "after the rotation")
	third := hub.waitTurn("crimson", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= rotatedAt && tn.SessionID != first.SessionID && strings.Contains(tn.ResultText, "MISSION")
	})
	if !strings.Contains(third.ResultText, hubSection) || strings.Contains(third.ResultText, "team red") {
		t.Fatalf("after the team's delete the loop was shown:\n%s\nwant the hub's section alone:\n%s", third.ResultText, hubSection)
	}
}
