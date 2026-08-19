//go:build integration

package itest

import (
	"strings"
	"testing"
)

type secretView struct {
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *server) secrets(loop string) []secretView {
	s.t.Helper()
	var v []secretView
	s.mustJSON("GET", "/api/loops/"+loop+"/secrets", nil, &v)
	return v
}

// TestLoopSecrets drives the per-loop secrets sub-resource end to end: names
// are visible but values never echo, PUT upserts in place, malformed names and
// empty values are rejected server-side, and DELETE removes one.
func TestLoopSecrets(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("vault", nil)

	if got := s.secrets("vault"); len(got) != 0 {
		t.Fatalf("fresh loop has %d secrets, want none", len(got))
	}

	const value = "ghp_itestSECRETvalue0123456789"

	// a well-formed secret stores; the value never appears in the response
	resp, body := s.do("PUT", "/api/loops/vault/secrets/GH_TOKEN", map[string]any{"value": value})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT secret: status %d (%s)", resp.StatusCode, body)
	}
	if strings.Contains(string(body), value) {
		t.Fatalf("PUT response echoed the secret value: %s", body)
	}
	got := s.secrets("vault")
	if len(got) != 1 || got[0].Name != "GH_TOKEN" {
		t.Fatalf("after PUT: got %+v, want [GH_TOKEN]", got)
	}
	if _, listBody := s.do("GET", "/api/loops/vault/secrets", nil); strings.Contains(string(listBody), value) {
		t.Fatalf("GET secrets leaked the value: %s", listBody)
	}

	// malformed names are rejected without storing (all URL-path-safe here)
	for _, bad := range []string{"1leading", "has-dash", "has.dot"} {
		resp, body := s.do("PUT", "/api/loops/vault/secrets/"+bad, map[string]any{"value": "x"})
		if resp.StatusCode != 400 {
			t.Fatalf("name %q: status %d, want 400 (%s)", bad, resp.StatusCode, body)
		}
	}
	// an empty value is rejected too (DELETE is the way to remove)
	if resp, body := s.do("PUT", "/api/loops/vault/secrets/EMPTY", map[string]any{"value": ""}); resp.StatusCode != 400 {
		t.Fatalf("empty value: status %d, want 400 (%s)", resp.StatusCode, body)
	}
	if got := s.secrets("vault"); len(got) != 1 {
		t.Fatalf("a rejected request was stored: %+v", got)
	}

	// a second PUT on the same name upserts rather than duplicating
	s.mustJSON("PUT", "/api/loops/vault/secrets/GH_TOKEN", map[string]any{"value": "ghp_replacement"}, nil)
	if got := s.secrets("vault"); len(got) != 1 {
		t.Fatalf("re-PUT duplicated the secret: %+v", got)
	}

	// a distinct secret adds a row; DELETE removes exactly one
	s.mustJSON("PUT", "/api/loops/vault/secrets/API_KEY", map[string]any{"value": "k"}, nil)
	if got := s.secrets("vault"); len(got) != 2 {
		t.Fatalf("want 2 secrets, got %+v", got)
	}
	s.mustJSON("DELETE", "/api/loops/vault/secrets/GH_TOKEN", nil, nil)
	got = s.secrets("vault")
	if len(got) != 1 || got[0].Name != "API_KEY" {
		t.Fatalf("after delete: got %+v, want only API_KEY", got)
	}
}
