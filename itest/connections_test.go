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
	HasSecret bool     `json:"has_secret"`
	CreatedAt int64    `json:"created_at"`
	Loops     []string `json:"loops"`
}

type loopConnectionJSON struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// loopConnections is the loop view's connections field.
func (s *server) loopConnections(loop string) []loopConnectionJSON {
	s.t.Helper()
	var view struct {
		Connections []loopConnectionJSON `json:"connections"`
	}
	s.mustJSON("GET", "/api/loops/"+loop, nil, &view)
	return view.Connections
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
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": value,
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
	if github.Kind != "env-var" || !github.HasSecret || github.Config.Env != "GH_TOKEN" || github.CreatedAt == 0 {
		t.Errorf("github = %+v, want an env-var on GH_TOKEN with a secret", github)
	}
	keep("GET", "/api/connections/github", nil, 200)

	s.wantRefusal("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "other",
	}, 409, "connection_exists")
	for _, refused := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"name": "Git_Hub", "kind": "env-var", "config": map[string]any{"env": "X"}, "secret": "s"}, "connection_name_invalid"},
		{map[string]any{"name": "app", "kind": "github-app", "config": map[string]any{"env": "X"}, "secret": "s"}, "connection_kind_invalid"},
		{map[string]any{"name": "env", "kind": "env-var", "config": map[string]any{"env": "GH-TOKEN"}, "secret": "s"}, "connection_config_invalid"},
		{map[string]any{"name": "mcp", "kind": "mcp-server", "config": map[string]any{"transport": "http", "url": "ftp://example.test"}}, "connection_config_invalid"},
		{map[string]any{"name": "bare", "kind": "env-var", "config": map[string]any{"env": "X"}}, "connection_secret_invalid"},
	} {
		s.wantRefusal("POST", "/api/connections", refused.body, 400, refused.code)
	}
	s.wantRefusal("GET", "/api/connections/nowhere", nil, 404, "connection_not_found")

	// Stored is known: a message carrying the value keeps only its
	// variable's name.
	s.mustJSON("POST", "/api/channels/group/messages", map[string]any{"text": "the key is " + value}, nil)
	var said []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(keep("GET", "/api/channels/group/messages", nil, 200), &said); err != nil {
		t.Fatal(err)
	}
	if len(said) != 1 || said[0].Text != "the key is <redacted:GH_TOKEN>" {
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

// The operator attaches a connection to loops and detaches it again; the
// connection lists its loops and each loop its connections, by name and
// kind and never by value. An attached connection can't be deleted until
// it is detached, and a deleted loop takes the ones only it held (ADR-0043).
func TestConnectionAttachments(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}
	const value = "ghp_fixtureATTACHEDsecret0000"
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": value,
	}, nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "docs", "kind": "mcp-server", "config": map[string]any{"transport": "http", "url": "https://mcp.example.test/"},
	}, nil)

	if got := s.loopConnections("aster"); got == nil || len(got) != 0 {
		t.Fatalf("a fresh loop's connections = %#v, want []", got)
	}
	for _, path := range []string{
		"/api/loops/aster/connections/github",
		"/api/loops/aster/connections/github", // again: nothing changes
		"/api/loops/briar/connections/github",
		"/api/loops/aster/connections/docs",
	} {
		if resp, body := s.do("PUT", path, nil); resp.StatusCode != 204 {
			t.Fatalf("PUT %s = %d %s, want 204", path, resp.StatusCode, body)
		}
	}
	s.wantRefusal("PUT", "/api/loops/aster/connections/nowhere", nil, 404, "connection_not_found")
	s.wantRefusal("PUT", "/api/loops/nobody/connections/github", nil, 404, "")

	var github connectionJSON
	s.mustJSON("GET", "/api/connections/github", nil, &github)
	if !reflect.DeepEqual(github.Loops, []string{"aster", "briar"}) {
		t.Errorf("github's loops = %v, want [aster briar]", github.Loops)
	}
	want := []loopConnectionJSON{{"docs", "mcp-server"}, {"github", "env-var"}}
	if got := s.loopConnections("aster"); !reflect.DeepEqual(got, want) {
		t.Errorf("aster's connections = %+v, want %+v", got, want)
	}
	for _, path := range []string{"/api/loops/aster", "/api/loops", "/api/connections"} {
		if _, body := s.do("GET", path, nil); strings.Contains(string(body), value) {
			t.Fatalf("GET %s carried the attached connection's secret: %s", path, body)
		}
	}

	s.wantRefusal("DELETE", "/api/connections/github", nil, 409, "connection_attached")
	for _, path := range []string{"/api/loops/aster/connections/github", "/api/loops/aster/connections/github"} {
		if resp, body := s.do("DELETE", path, nil); resp.StatusCode != 204 {
			t.Fatalf("DELETE %s = %d %s, want 204", path, resp.StatusCode, body)
		}
	}
	if got := s.loopConnections("aster"); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("aster's connections after detaching github = %+v, want only docs", got)
	}
	s.wantRefusal("DELETE", "/api/connections/github", nil, 409, "connection_attached") // briar still holds it

	// briar held it last, so it goes with briar; docs, which aster holds, stays.
	s.mustJSON("DELETE", "/api/loops/briar", nil, nil)
	s.wantRefusal("GET", "/api/connections/github", nil, 404, "connection_not_found")
	if got := s.loopConnections("aster"); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("aster's connections after briar was deleted = %+v, want only docs", got)
	}
}
