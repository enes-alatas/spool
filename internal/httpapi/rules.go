package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// Fleet rule caps (ADR-0024). Title and body bound one rule; the section cap
// bounds what every loop pays on every wake — about a thousand tokens — and
// is measured on the rendered section, header and conflict line included,
// so it protects the prompt rather than approximating it.
const (
	maxRuleTitleChars    = 120
	maxRuleBodyChars     = 1200
	maxRulesSectionChars = 4000

	codeRulesTooLarge = "rules_too_large"
)

// rulesBudget is what the section cap measures and where it stands, sent
// with every read and write so a client can show the wall while the
// operator is typing instead of a 400 after the save.
type rulesBudget struct {
	// SectionChars is the section a loop in no team is shown: the hub's
	// rules alone.
	SectionChars    int `json:"section_chars"`
	SectionCharsMax int `json:"section_chars_max"`
	TitleCharsMax   int `json:"title_chars_max"`
	BodyCharsMax    int `json:"body_chars_max"`
	// Teams is the section each team's loops are shown, the hub's rules
	// then the team's, one entry per team by name (ADR-0066).
	Teams []teamBudget `json:"teams"`
}

type teamBudget struct {
	Team         string `json:"team"`
	SectionChars int    `json:"section_chars"`
}

// budgetOf measures the section each scope's loops are shown: a loop in no
// team, and a loop in each of teams.
func budgetOf(rules []*store.FleetRule, teams []string) rulesBudget {
	budget := rulesBudget{
		SectionChars:    sectionChars(rules, ""),
		SectionCharsMax: maxRulesSectionChars,
		TitleCharsMax:   maxRuleTitleChars,
		BodyCharsMax:    maxRuleBodyChars,
		Teams:           make([]teamBudget, 0, len(teams)),
	}
	for _, team := range teams {
		budget.Teams = append(budget.Teams, teamBudget{Team: team, SectionChars: sectionChars(rules, team)})
	}
	return budget
}

// sectionChars is the length of the section a loop in team is shown, ""
// for a loop in no team.
func sectionChars(rules []*store.FleetRule, team string) int {
	return utf8.RuneCountInString(loop.FleetRulesSection(loop.RulesFor(team, rules)))
}

// teamNames lists the hub's teams by name, as the budget measures them.
func (server *Server) teamNames(ctx context.Context) ([]string, error) {
	teams, err := server.Store.Teams().List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(teams))
	for _, team := range teams {
		names = append(names, team.Name)
	}
	return names, nil
}

// ruleTeamRefused refuses a rule scoped to a team the hub doesn't have,
// and reports whether it did.
func (server *Server) ruleTeamRefused(w http.ResponseWriter, team string, teams []string) bool {
	if team == "" || slices.Contains(teams, team) {
		return false
	}
	server.jsonErrCode(w, 404, codeTeamNotFound, "team %q not found", team)
	return true
}

type rulesView struct {
	Rules  []*store.FleetRule `json:"rules"`
	Budget rulesBudget        `json:"budget"`
}

// ruleWriteView answers a successful write: the rule, plus the budget the
// write moved, so the client's counter is true without a second round trip.
type ruleWriteView struct {
	Rule   *store.FleetRule `json:"rule"`
	Budget rulesBudget      `json:"budget"`
}

func (server *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := server.Store.FleetRules().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if rules == nil {
		rules = []*store.FleetRule{}
	}
	teams, err := server.teamNames(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, rulesView{Rules: rules, Budget: budgetOf(rules, teams)})
}

type ruleReq struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
	// Enabled left out on create means live: a rule is written to be
	// followed. On PATCH, nil leaves it unchanged like the other fields.
	Enabled *bool `json:"enabled"`
	// Team scopes the rule to one team's loops: "" or absent on create is
	// a hub rule, and nil on PATCH leaves the scope (ADR-0066).
	Team *string `json:"team"`
}

func (server *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	now := time.Now().UnixMilli()
	rule := &store.FleetRule{ID: ruleID(), Enabled: true, CreatedAt: now, UpdatedAt: now}
	if req.Title != nil {
		rule.Title = strings.TrimSpace(*req.Title)
	}
	if req.Body != nil {
		rule.Body = strings.TrimSpace(*req.Body)
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.Team != nil {
		rule.Team = *req.Team
	}
	if err := validateRuleText(rule.Title, rule.Body); err != nil {
		server.jsonErr(w, 400, "%v", err)
		return
	}

	server.rulesMu.Lock()
	defer server.rulesMu.Unlock()
	current, err := server.Store.FleetRules().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	teams, err := server.teamNames(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if server.ruleTeamRefused(w, rule.Team, teams) {
		return
	}
	proposed := append(append([]*store.FleetRule{}, current...), rule)
	if !server.withinRulesBudget(w, current, proposed, teams) {
		return
	}
	if err := server.Store.FleetRules().Create(r.Context(), rule); err != nil {
		if errors.Is(err, store.ErrNotFound) { // the team went since it was read
			server.jsonErrCode(w, 404, codeTeamNotFound, "team %q not found", rule.Team)
			return
		}
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 201, ruleWriteView{Rule: rule, Budget: budgetOf(proposed, teams)})
}

func (server *Server) handlePatchRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}

	server.rulesMu.Lock()
	defer server.rulesMu.Unlock()
	current, err := server.Store.FleetRules().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	var rule *store.FleetRule
	proposed := make([]*store.FleetRule, 0, len(current))
	for _, existing := range current {
		if existing.ID != r.PathValue("id") {
			proposed = append(proposed, existing)
			continue
		}
		edited := *existing
		rule = &edited
		proposed = append(proposed, rule)
	}
	if rule == nil {
		server.jsonErr(w, 404, "rule not found")
		return
	}
	if req.Title != nil {
		rule.Title = strings.TrimSpace(*req.Title)
	}
	if req.Body != nil {
		rule.Body = strings.TrimSpace(*req.Body)
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.Team != nil {
		rule.Team = *req.Team
	}
	if err := validateRuleText(rule.Title, rule.Body); err != nil {
		server.jsonErr(w, 400, "%v", err)
		return
	}
	teams, err := server.teamNames(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if server.ruleTeamRefused(w, rule.Team, teams) {
		return
	}
	if !server.withinRulesBudget(w, current, proposed, teams) {
		return
	}
	rule.UpdatedAt = time.Now().UnixMilli()
	if err := server.Store.FleetRules().Update(r.Context(), rule); err != nil {
		server.storeErr(w, err, "rule")
		return
	}
	writeJSON(w, 200, ruleWriteView{Rule: rule, Budget: budgetOf(proposed, teams)})
}

func (server *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	server.rulesMu.Lock()
	defer server.rulesMu.Unlock()
	if _, err := server.Store.FleetRules().Get(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			server.jsonErr(w, 404, "rule not found")
		} else {
			server.jsonErr(w, 500, "%v", err)
		}
		return
	}
	if err := server.Store.FleetRules().Delete(r.Context(), r.PathValue("id")); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	w.WriteHeader(204)
}

func validateRuleText(title, body string) error {
	switch {
	case title == "":
		return fmt.Errorf("rule title is empty")
	case utf8.RuneCountInString(title) > maxRuleTitleChars:
		return fmt.Errorf("rule title too long (max %d chars)", maxRuleTitleChars)
	case body == "":
		return fmt.Errorf("rule body is empty")
	case utf8.RuneCountInString(body) > maxRuleBodyChars:
		return fmt.Errorf("rule body too long (max %d chars)", maxRuleBodyChars)
	}
	return nil
}

// withinRulesBudget applies the section cap to a proposed rule set, for
// every section a loop is shown: a loop in no team's, and each team's
// (ADR-0066). So a hub rule is checked against every team, and a
// team rule, which changes its own team's section alone, against that one.
// A write that leaves a section over the cap and larger than it was is
// rejected with the budget it would have produced, naming whose section it
// is. A write that shrinks an oversized section always goes through, so an
// operator is never locked out of the fix. Callers hold rulesMu so the
// read-check-write cannot interleave.
func (server *Server) withinRulesBudget(w http.ResponseWriter, current, proposed []*store.FleetRule, teams []string) bool {
	before, after := budgetOf(current, teams), budgetOf(proposed, teams)
	over, whose := 0, "a loop in no team"
	if after.SectionChars > maxRulesSectionChars && after.SectionChars > before.SectionChars {
		over = after.SectionChars
	}
	for i, team := range after.Teams {
		if over == 0 && team.SectionChars > maxRulesSectionChars && team.SectionChars > before.Teams[i].SectionChars {
			over, whose = team.SectionChars, fmt.Sprintf("team %s's loops", team.Team)
		}
	}
	if over == 0 {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(struct {
		Error  string      `json:"error"`
		Code   string      `json:"code"`
		Budget rulesBudget `json:"budget"`
	}{
		Error: fmt.Sprintf("enabled rules would render to %d chars for %s, over the %d-char section cap",
			over, whose, maxRulesSectionChars),
		Code:   codeRulesTooLarge,
		Budget: after,
	})
	return false
}
