//go:build integration

package itest

import (
	"strings"
	"testing"
)

type settingsView struct {
	ClaudeTokenSet bool `json:"claude_token_set"`
}

func (s *server) settings() settingsView {
	s.t.Helper()
	var v settingsView
	s.mustJSON("GET", "/api/settings", nil, &v)
	return v
}

// TestSettingsClaudeToken drives the operator setup-token endpoint end to end:
// malformed pastes are rejected server-side, a well-formed token stores as
// presence-only, its value never appears in a response, and "" clears it.
func TestSettingsClaudeToken(t *testing.T) {
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
