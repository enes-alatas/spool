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
