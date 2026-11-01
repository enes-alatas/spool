//go:build integration

package itest

import (
	"strings"
	"testing"
)

type settingsView struct {
	ClaudeTokenSet bool   `json:"claude_token_set"`
	ClaudeVersion  string `json:"claude_version"`
}

func (s *server) settings() settingsView {
	s.t.Helper()
	var v settingsView
	s.mustJSON("GET", "/api/settings", nil, &v)
	return v
}

// TestSettingsReportsClaudeVersion: the CLI the hub found at start is served
// to the operator, behind the credential (#258). It describes the host, so it
// is not health's to answer.
func TestSettingsReportsClaudeVersion(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	if got := s.settings().ClaudeVersion; !strings.Contains(got, "fakeclaude") {
		t.Errorf("claude_version = %q, want the version fakeclaude reports", got)
	}
}

// TestSettingsClaudeToken drives the operator setup-token endpoint end to end:
// malformed pastes are rejected server-side, a well-formed token stores as
// presence-only, its value never appears in a response, and "" clears it.
func TestSettingsClaudeToken(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	const token = "sk-ant-oat01-itesttokenABCDEFGHIJKLMNOP0123456789"

	if s.settings().ClaudeTokenSet {
		t.Fatal("fresh server reports a token already set")
	}

	// malformed pastes fail the format check without a network call
	for _, bad := range []string{"not-a-token-value-xxxxxxxxxx", "sk-ant-oat", "sk-ant-oat01 with a space here"} {
		resp, body := s.do("PUT", "/api/settings", map[string]any{"claude_oauth_token": bad})
		if resp.StatusCode != 400 {
			t.Fatalf("token %q: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	if s.settings().ClaudeTokenSet {
		t.Fatal("a rejected token was stored")
	}

	// a well-formed token stores; presence flips true but the value never echoes
	resp, body := s.do("PUT", "/api/settings", map[string]any{"claude_oauth_token": token})
	if resp.StatusCode != 200 {
		t.Fatalf("valid token: status %d (%s)", resp.StatusCode, body)
	}
	if strings.Contains(string(body), token) {
		t.Fatalf("PUT response echoed the token value: %s", body)
	}
	if !s.settings().ClaudeTokenSet {
		t.Fatal("token not reported set after storing")
	}
	if _, getBody := s.do("GET", "/api/settings", nil); strings.Contains(string(getBody), token) {
		t.Fatalf("GET /api/settings leaked the token value: %s", getBody)
	}

	// empty clears it
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": ""}, nil)
	if s.settings().ClaudeTokenSet {
		t.Fatal("token still set after clearing")
	}
}

// TestSettingsRotationThresholds drives the context-rotation thresholds
// (ADR-0022): defaults come back effective, valid pairs store, and an
// inverted or out-of-range pair is rejected without clobbering the stored one.
func TestSettingsRotationThresholds(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())

	var v struct {
		Arm   int `json:"context_arm_percent"`
		Force int `json:"context_force_percent"`
	}
	s.mustJSON("GET", "/api/settings", nil, &v)
	if v.Arm != 40 || v.Force != 70 {
		t.Fatalf("defaults = %d/%d, want 40/70", v.Arm, v.Force)
	}

	s.mustJSON("PUT", "/api/settings", map[string]any{"context_arm_percent": 30, "context_force_percent": 55}, &v)
	if v.Arm != 30 || v.Force != 55 {
		t.Fatalf("after PUT = %d/%d, want 30/55", v.Arm, v.Force)
	}

	for _, bad := range []map[string]any{
		{"context_arm_percent": 60},                               // inverted against the stored force of 55
		{"context_arm_percent": 0, "context_force_percent": 50},   // out of range
		{"context_arm_percent": 50, "context_force_percent": 100}, // out of range
		{"context_arm_percent": 80, "context_force_percent": 20},  // inverted
	} {
		resp, body := s.do("PUT", "/api/settings", bad)
		if resp.StatusCode != 400 {
			t.Fatalf("PUT %v: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}

	s.mustJSON("GET", "/api/settings", nil, &v)
	if v.Arm != 30 || v.Force != 55 {
		t.Fatalf("rejected writes changed stored thresholds to %d/%d", v.Arm, v.Force)
	}
}

// The health check's error-turn threshold (ADR-0053) comes back as its
// default, stores a count, turns off at 0, and refuses one out of range
// without touching the stored one.
func TestSettingsHealthErrorTurns(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())

	var v struct {
		Turns int `json:"health_error_turns"`
	}
	s.mustJSON("GET", "/api/settings", nil, &v)
	if v.Turns != 3 {
		t.Fatalf("default = %d, want 3", v.Turns)
	}
	for _, turns := range []int{5, 0} {
		s.mustJSON("PUT", "/api/settings", map[string]any{"health_error_turns": turns}, &v)
		if v.Turns != turns {
			t.Fatalf("after PUT %d = %d", turns, v.Turns)
		}
	}
	for _, bad := range []int{-1, 101} {
		if resp, body := s.do("PUT", "/api/settings", map[string]any{"health_error_turns": bad}); resp.StatusCode != 400 {
			t.Fatalf("PUT %d: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	s.mustJSON("GET", "/api/settings", nil, &v)
	if v.Turns != 0 {
		t.Fatalf("rejected writes changed the stored threshold to %d", v.Turns)
	}
}

// The turn_stuck limit (#1142) comes back as its default, stores minutes,
// turns off at 0, and refuses one out of range without touching the stored
// one.
func TestSettingsHealthTurnMinutes(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())

	var settings struct {
		Minutes int `json:"health_turn_minutes"`
	}
	hub.mustJSON("GET", "/api/settings", nil, &settings)
	if settings.Minutes != 120 {
		t.Fatalf("default = %d, want 120", settings.Minutes)
	}
	for _, minutes := range []int{30, 0} {
		hub.mustJSON("PUT", "/api/settings", map[string]any{"health_turn_minutes": minutes}, &settings)
		if settings.Minutes != minutes {
			t.Fatalf("after PUT %d = %d", minutes, settings.Minutes)
		}
	}
	for _, bad := range []int{-1, 1441} {
		if resp, body := hub.do("PUT", "/api/settings", map[string]any{"health_turn_minutes": bad}); resp.StatusCode != 400 {
			t.Fatalf("PUT %d: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	hub.mustJSON("GET", "/api/settings", nil, &settings)
	if settings.Minutes != 0 {
		t.Fatalf("rejected writes changed the stored limit to %d", settings.Minutes)
	}
}

// The wake_missed grace reads 10 minutes until set, takes 0 to 1440 with 0
// for off, and refuses anything else without changing what is stored
// (#1143).
func TestSettingsHealthWakeGraceMinutes(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())

	var settings struct {
		Minutes int `json:"health_wake_grace_minutes"`
	}
	hub.mustJSON("GET", "/api/settings", nil, &settings)
	if settings.Minutes != 10 {
		t.Fatalf("default = %d, want 10", settings.Minutes)
	}
	for _, minutes := range []int{30, 1440, 0} {
		hub.mustJSON("PUT", "/api/settings", map[string]any{"health_wake_grace_minutes": minutes}, &settings)
		if settings.Minutes != minutes {
			t.Fatalf("after PUT %d = %d", minutes, settings.Minutes)
		}
	}
	for _, bad := range []int{-1, 1441} {
		if resp, body := hub.do("PUT", "/api/settings", map[string]any{"health_wake_grace_minutes": bad}); resp.StatusCode != 400 {
			t.Fatalf("PUT %d: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	hub.mustJSON("GET", "/api/settings", nil, &settings)
	if settings.Minutes != 0 {
		t.Fatalf("rejected writes changed the stored grace to %d", settings.Minutes)
	}
}
