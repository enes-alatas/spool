//go:build integration

package itest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type connectionJSON struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Config struct {
		Env       string   `json:"env"`
		Transport string   `json:"transport"`
		URL       string   `json:"url"`
		Command   string   `json:"command"`
		Args      []string `json:"args"`
	} `json:"config"`
	HasSecret bool  `json:"has_secret"`
	CreatedAt int64 `json:"created_at"`
}

// The operator defines a connection once, reads it back without its secret,
// and deletes it; a malformed one is refused with a code saying which part
// is wrong. The secret is redacted from the moment it is stored, before
// any loop is given it (ADR-0043).
func TestConnectionsRoundTrip(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)

	const value = "ghp_fixtureCONNECTIONsecret0000"
	var bodies []string
	keep := func(method, path string, body any, status int) []byte {
		t.Helper()
		resp, got := s.do(method, path, body)
		if resp.StatusCode != status {
			t.Fatalf("%s %s = %d %s, want %d", method, path, resp.StatusCode, got, status)
		}
		bodies = append(bodies, string(got))
		return got
	}

	keep("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-credential", "config": map[string]any{"env": "GH_TOKEN"}, "secret": value,
	}, 201)
	keep("POST", "/api/connections", map[string]any{
		"name": "docs", "kind": "mcp-server", "config": map[string]any{"transport": "stdio", "command": "docs-mcp", "args": []string{"--read-only"}},
	}, 201)

	var list []connectionJSON
	if err := json.Unmarshal(keep("GET", "/api/connections", nil, 200), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "docs" || list[1].Name != "github" {
		t.Fatalf("list = %+v, want [docs github]", list)
	}
	docs, github := list[0], list[1]
	if docs.Kind != "mcp-server" || docs.HasSecret || docs.Config.Transport != "stdio" ||
		docs.Config.Command != "docs-mcp" || !reflect.DeepEqual(docs.Config.Args, []string{"--read-only"}) {
		t.Errorf("docs = %+v, want a stdio mcp-server with no secret", docs)
	}
	if github.Kind != "env-credential" || !github.HasSecret || github.Config.Env != "GH_TOKEN" || github.CreatedAt == 0 {
		t.Errorf("github = %+v, want an env-credential on GH_TOKEN with a secret", github)
	}
	keep("GET", "/api/connections/github", nil, 200)

	s.wantRefusal("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-credential", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "other",
	}, 409, "connection_exists")
	for _, refused := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"name": "Git_Hub", "kind": "env-credential", "config": map[string]any{"env": "X"}, "secret": "s"}, "connection_name_invalid"},
		{map[string]any{"name": "app", "kind": "github-app", "config": map[string]any{"env": "X"}, "secret": "s"}, "connection_kind_invalid"},
		{map[string]any{"name": "env", "kind": "env-credential", "config": map[string]any{"env": "GH-TOKEN"}, "secret": "s"}, "connection_config_invalid"},
		{map[string]any{"name": "mcp", "kind": "mcp-server", "config": map[string]any{"transport": "http", "url": "ftp://example.test"}}, "connection_config_invalid"},
		{map[string]any{"name": "bare", "kind": "env-credential", "config": map[string]any{"env": "X"}}, "connection_secret_invalid"},
	} {
		s.wantRefusal("POST", "/api/connections", refused.body, 400, refused.code)
	}
	s.wantRefusal("GET", "/api/connections/nowhere", nil, 404, "connection_not_found")

	// Stored is known: a message carrying the value keeps only its name.
	s.mustJSON("POST", "/api/channels/group/messages", map[string]any{"text": "the key is " + value}, nil)
	var said []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(keep("GET", "/api/channels/group/messages", nil, 200), &said); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || said[0].Text != "the key is <redacted:connection:github>" {
		t.Errorf("a connection's secret was not redacted from a message: %+v", said)
	}

	keep("DELETE", "/api/connections/docs", nil, 200)
	s.wantRefusal("DELETE", "/api/connections/docs", nil, 404, "connection_not_found")
	s.mustJSON("GET", "/api/connections", nil, &list)
	if len(list) != 1 || list[0].Name != "github" {
		t.Errorf("after deleting docs: %+v, want [github]", list)
	}

	for _, body := range bodies {
		if strings.Contains(body, value) {
			t.Fatalf("a response carried the connection's secret: %s", body)
		}
	}
}
