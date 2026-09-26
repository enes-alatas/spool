package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	SectionChars    int `json:"section_chars"`
	SectionCharsMax int `json:"section_chars_max"`
	TitleCharsMax   int `json:"title_chars_max"`
	BodyCharsMax    int `json:"body_chars_max"`
}

func budgetOf(rules []*store.FleetRule) rulesBudget {
	return rulesBudget{
		SectionChars:    utf8.RuneCountInString(loop.FleetRulesSection(rules)),
		SectionCharsMax: maxRulesSectionChars,
		TitleCharsMax:   maxRuleTitleChars,
		BodyCharsMax:    maxRuleBodyChars,
	}
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

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Store.FleetRules().List(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if rules == nil {
		rules = []*store.FleetRule{}
	}
	writeJSON(w, 200, rulesView{Rules: rules, Budget: budgetOf(rules)})
}

type ruleReq struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
	// Enabled left out on create means live: a rule is written to be
	// followed. On PATCH, nil leaves it unchanged like the other fields.
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
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
	if err := validateRuleText(rule.Title, rule.Body); err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}

	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	current, err := s.Store.FleetRules().List(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	proposed := append(append([]*store.FleetRule{}, current...), rule)
	if !s.withinRulesBudget(w, current, proposed) {
		return
	}
	if err := s.Store.FleetRules().Create(r.Context(), rule); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 201, ruleWriteView{Rule: rule, Budget: budgetOf(proposed)})
}

func (s *Server) handlePatchRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}

	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	current, err := s.Store.FleetRules().List(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
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
		s.jsonErr(w, 404, "rule not found")
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
	if err := validateRuleText(rule.Title, rule.Body); err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}
	if !s.withinRulesBudget(w, current, proposed) {
		return
	}
	rule.UpdatedAt = time.Now().UnixMilli()
	if err := s.Store.FleetRules().Update(r.Context(), rule); err != nil {
		s.storeErr(w, err, "rule")
		return
	}
	writeJSON(w, 200, ruleWriteView{Rule: rule, Budget: budgetOf(proposed)})
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	s.rulesMu.Lock()
	defer s.rulesMu.Unlock()
	if _, err := s.Store.FleetRules().Get(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.jsonErr(w, 404, "rule not found")
		} else {
			s.jsonErr(w, 500, "%v", err)
		}
		return
	}
	if err := s.Store.FleetRules().Delete(r.Context(), r.PathValue("id")); err != nil {
		s.jsonErr(w, 500, "%v", err)
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

// withinRulesBudget applies the section cap to a proposed rule set: a write
// that leaves the rendered section over the cap and larger than it was is
// rejected with the budget it would have produced. A write that shrinks an
// oversized section always goes through, so an operator is never locked out
// of the fix. Callers hold rulesMu so the read-check-write cannot interleave.
func (s *Server) withinRulesBudget(w http.ResponseWriter, current, proposed []*store.FleetRule) bool {
	before, after := budgetOf(current), budgetOf(proposed)
	if after.SectionChars <= maxRulesSectionChars || after.SectionChars <= before.SectionChars {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(struct {
		Error  string      `json:"error"`
		Code   string      `json:"code"`
		Budget rulesBudget `json:"budget"`
	}{
		Error: fmt.Sprintf("enabled rules would render to %d chars, over the %d-char section cap",
			after.SectionChars, maxRulesSectionChars),
		Code:   codeRulesTooLarge,
		Budget: after,
	})
	return false
}
