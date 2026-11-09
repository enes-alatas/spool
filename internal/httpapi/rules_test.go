package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestValidateRuleText(t *testing.T) {
	cases := []struct {
		name        string
		title, body string
		wantErr     bool
	}{
		{"well-formed", "sign your work", "End every artifact with your name.", false},
		{"multi-byte at the cap", strings.Repeat("é", maxRuleTitleChars), strings.Repeat("ü", maxRuleBodyChars), false},
		{"empty title", "", "body", true},
		{"empty body", "title", "", true},
		{"title over cap", strings.Repeat("x", maxRuleTitleChars+1), "body", true},
		{"body over cap", "title", strings.Repeat("x", maxRuleBodyChars+1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRuleText(tc.title, tc.body)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestWithinRulesBudget pins the section guard's one subtlety: it rejects
// only a write that leaves the section over the cap AND larger than before,
// so shrinking an oversized set is never refused.
func TestWithinRulesBudget(t *testing.T) {
	server := &Server{}
	big := func(size int) *store.FleetRule {
		return &store.FleetRule{Title: "t", Body: strings.Repeat("x", size), Enabled: true}
	}
	under := []*store.FleetRule{big(1000)}
	over := []*store.FleetRule{big(1200), big(1200), big(1200), big(1200)}
	lessOver := []*store.FleetRule{big(1200), big(1200), big(1200), big(1100)}

	if server.withinRulesBudget(httptest.NewRecorder(), under, over, nil) {
		t.Error("growing past the cap was allowed")
	}
	if !server.withinRulesBudget(httptest.NewRecorder(), over, lessOver, nil) {
		t.Error("shrinking an oversized section was refused")
	}
	if !server.withinRulesBudget(httptest.NewRecorder(), under, under, nil) {
		t.Error("an unchanged set under the cap was refused")
	}

	rec := httptest.NewRecorder()
	server.withinRulesBudget(rec, under, over, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"`+codeRulesTooLarge+`"`) {
		t.Fatalf("rejection = %d %s, want 400 with code %s", rec.Code, rec.Body.String(), codeRulesTooLarge)
	}
	if !strings.Contains(rec.Body.String(), `"section_chars_max":4000`) {
		t.Fatalf("rejection carries no budget: %s", rec.Body.String())
	}
}

// A rule scoped to a team counts toward that team's section alone, and a
// hub rule toward every team's (ADR-0066): a write that would push
// one team's section over the cap is refused naming that team, while a loop
// in no team still has room.
func TestWithinRulesBudgetChecksEachTeam(t *testing.T) {
	server := &Server{}
	rule := func(team string, size int) *store.FleetRule {
		return &store.FleetRule{Title: "t", Body: strings.Repeat("x", size), Enabled: true, Team: team}
	}
	teams := []string{"blue", "red"}
	current := []*store.FleetRule{rule("", 1200), rule("red", 1200), rule("red", 1200)}

	rec := httptest.NewRecorder()
	if server.withinRulesBudget(rec, current, append(current, rule("", 1000)), teams) {
		t.Fatal("a hub rule that pushes team red's section over the cap was allowed")
	}
	if body := rec.Body.String(); !strings.Contains(body, "team red's loops") {
		t.Errorf("the refusal %s doesn't name team red", body)
	}
	if !server.withinRulesBudget(httptest.NewRecorder(), current, append(current, rule("blue", 1200)), teams) {
		t.Error("a team rule for blue was refused for red's section")
	}
	if server.withinRulesBudget(httptest.NewRecorder(), current, append(current, rule("red", 1200)), teams) {
		t.Error("a team rule that pushes its own team's section over the cap was allowed")
	}

	budget := budgetOf(current, teams)
	if len(budget.Teams) != 2 || budget.Teams[0].Team != "blue" || budget.Teams[1].Team != "red" {
		t.Fatalf("the budget's teams are %+v, want blue then red", budget.Teams)
	}
	if budget.Teams[0].SectionChars != budget.SectionChars || budget.Teams[1].SectionChars <= budget.SectionChars {
		t.Errorf("budget %+v: blue's section should equal the hub's and red's be larger", budget)
	}
}
