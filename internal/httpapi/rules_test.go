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
	s := &Server{}
	big := func(n int) *store.FleetRule {
		return &store.FleetRule{Title: "t", Body: strings.Repeat("x", n), Enabled: true}
	}
	under := []*store.FleetRule{big(1000)}
	over := []*store.FleetRule{big(1200), big(1200), big(1200), big(1200)}
	lessOver := []*store.FleetRule{big(1200), big(1200), big(1200), big(1100)}

	if s.withinRulesBudget(httptest.NewRecorder(), under, over) {
		t.Error("growing past the cap was allowed")
	}
	if !s.withinRulesBudget(httptest.NewRecorder(), over, lessOver) {
		t.Error("shrinking an oversized section was refused")
	}
	if !s.withinRulesBudget(httptest.NewRecorder(), under, under) {
		t.Error("an unchanged set under the cap was refused")
	}

	rec := httptest.NewRecorder()
	s.withinRulesBudget(rec, under, over)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"code":"`+codeRulesTooLarge+`"`) {
		t.Fatalf("rejection = %d %s, want 400 with code %s", rec.Code, rec.Body.String(), codeRulesTooLarge)
	}
	if !strings.Contains(rec.Body.String(), `"section_chars_max":4000`) {
		t.Fatalf("rejection carries no budget: %s", rec.Body.String())
	}
}
