//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// checkJSON is POST /api/connections/{name}/check.
type checkJSON struct {
	Connection string `json:"connection"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	SecretSent bool   `json:"secret_sent"`
	CheckedAt  int64  `json:"checked_at"`
}

// An admin checks a connection after saving it (#1323). An http MCP
// server is sent an initialize, with its secret only once a loop holding
// it would have the hub send it there; a loopback one no bare loop holds
// is left alone, since a contained loop's runs in its workstation. A
// github-app mints a token. A member can't check, the secret is in no
// answer and no log line, and a revoked connection is refused.
func TestAConnectionIsChecked(t *testing.T) {
	t.Parallel()
	const secret = "fixture-mcp-CHECKsecret-0000"
	var mu sync.Mutex
	var auth []string
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}}`))
	}))
	t.Cleanup(mcp.Close)
	fake, api := startFakeGitHubApps(t)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--github-api-base", api)
	hub.createLoop("aster", nil)

	check := func(name string) checkJSON {
		t.Helper()
		resp, body := hub.do("POST", "/api/connections/"+name+"/check", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("check %s = %d %s", name, resp.StatusCode, body)
		}
		if strings.Contains(string(body), secret) || strings.Contains(string(body), "PRIVATE KEY") {
			t.Fatalf("a check's answer carries a secret: %s", body)
		}
		var view checkJSON
		if err := json.Unmarshal(body, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	sent := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := auth
		auth = nil
		return out
	}

	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "docs", "kind": "mcp-server", "secret": secret,
		"config": map[string]any{"transport": "http", "url": mcp.URL + "/mcp"},
	}, nil)
	// held by no loop, a loopback server is nobody's to check yet
	if view := check("docs"); view.Status != "skipped" || view.Reason != "workstation_local" || len(sent()) != 0 {
		t.Fatalf("an unheld loopback server: %+v, want skipped workstation_local and nothing sent", view)
	}
	// a bare loop holding it has the hub broker it, secret and all
	hub.mustJSON("PUT", "/api/loops/aster/connections/docs", nil, nil)
	if view := check("docs"); view.Status != "ok" || !view.SecretSent || view.CheckedAt == 0 {
		t.Fatalf("a held server: %+v, want ok with the secret sent", view)
	}
	if got := sent(); len(got) != 1 || got[0] != "Bearer "+secret {
		t.Fatalf("the server was sent %d requests, want one initialize with the secret", len(got))
	}
	hub.mustJSON("PUT", "/api/connections/docs/secret", map[string]any{"value": "fixture-mcp-WRONGsecret-0000"}, nil)
	if view := check("docs"); view.Status != "failed" || view.Reason != "secret_refused" {
		t.Fatalf("a wrong secret: %+v, want failed secret_refused", view)
	}
	sent()

	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "notes", "kind": "mcp-server", "config": map[string]any{"transport": "stdio", "command": "notes-mcp"},
	}, nil)
	if view := check("notes"); view.Status != "skipped" || view.Reason != "not_checkable" {
		t.Errorf("a stdio server: %+v, want skipped not_checkable", view)
	}

	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-app", "kind": "github-app", "secret": fake.newAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	if view := check("gh-app"); view.Status != "ok" || len(fake.minted()) != 1 {
		t.Errorf("a github-app: %+v with %d minted, want ok and one token", view, len(fake.minted()))
	}
	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "gh-stranger", "kind": "github-app", "secret": fake.strangerAppKey(),
		"config": map[string]any{"app_id": "1234", "installation_id": 42},
	}, nil)
	if view := check("gh-stranger"); view.Status != "failed" || view.Reason != "github_refused" || !strings.Contains(view.Message, "401") {
		t.Errorf("a github-app GitHub refuses: %+v, want failed github_refused naming 401", view)
	}

	member := hub.signedInAs("mia", "member")
	if resp, _ := hub.asCookie("POST", "/api/connections/docs/check", member, nil); resp.StatusCode != 403 {
		t.Errorf("a member's check = %d, want 403", resp.StatusCode)
	}
	if len(sent()) != 0 {
		t.Error("a member's refused check reached the server")
	}

	hub.mustJSON("POST", "/api/connections/notes/revoke", nil, nil)
	hub.wantRefusal("POST", "/api/connections/notes/check", nil, 409, "connection_revoked")
	hub.wantRefusal("POST", "/api/connections/nowhere/check", nil, 404, "connection_not_found")

	if log := hub.log(); strings.Contains(log, secret) || strings.Contains(log, mcp.URL) || !strings.Contains(log, "connection check") {
		t.Error("the hub's log carries a checked secret or url, or no line of the checks")
	}
}

// A connection keeps its last check (#1360): the list carries it to an
// admin as last_check, never to a member, and a new value or a revoke
// clears it, since it described the old one.
func TestAConnectionKeepsItsLastCheck(t *testing.T) {
	t.Parallel()
	const secret = "fixture-mcp-LASTcheck-0000"
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}}`))
	}))
	t.Cleanup(mcp.Close)
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare")
	hub.createLoop("aster", nil)

	type lastCheck struct {
		Status    string `json:"status"`
		Reason    string `json:"reason"`
		CheckedAt int64  `json:"checked_at"`
	}
	type listed struct {
		Name      string     `json:"name"`
		LastCheck *lastCheck `json:"last_check"`
	}
	last := func(cookie string) (*lastCheck, string) {
		t.Helper()
		var resp *http.Response
		var body []byte
		if cookie == "" {
			resp, body = hub.do("GET", "/api/connections", nil)
		} else {
			resp, body = hub.asCookie("GET", "/api/connections", cookie, nil)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("GET /api/connections = %d %s", resp.StatusCode, body)
		}
		var views []listed
		if err := json.Unmarshal(body, &views); err != nil {
			t.Fatal(err)
		}
		for _, view := range views {
			if view.Name == "docs" {
				return view.LastCheck, string(body)
			}
		}
		t.Fatalf("docs not listed: %s", body)
		return nil, ""
	}
	check := func() checkJSON {
		t.Helper()
		var view checkJSON
		hub.mustJSON("POST", "/api/connections/docs/check", nil, &view)
		return view
	}

	hub.mustJSON("POST", "/api/connections", map[string]any{
		"name": "docs", "kind": "mcp-server", "secret": secret,
		"config": map[string]any{"transport": "http", "url": mcp.URL + "/mcp"},
	}, nil)
	if got, body := last(""); got != nil || strings.Contains(body, "last_check") {
		t.Fatalf("a connection never checked lists %s, want no last_check", body)
	}
	hub.mustJSON("PUT", "/api/loops/aster/connections/docs", nil, nil)
	ran := check()
	if got, _ := last(""); got == nil || got.Status != "ok" || got.Reason != "ok" || got.CheckedAt != ran.CheckedAt {
		t.Fatalf("after an ok check, last_check = %+v, want ok at %d", got, ran.CheckedAt)
	}
	member := hub.signedInAs("mia", "member")
	if got, body := last(member); got != nil || strings.Contains(body, "last_check") {
		t.Errorf("a member reads a last check: %s", body)
	}

	hub.mustJSON("PUT", "/api/connections/docs/secret", map[string]any{"value": "fixture-mcp-WRONGlast-0000"}, nil)
	if got, _ := last(""); got != nil {
		t.Fatalf("after a rotate, last_check = %+v, want none", got)
	}
	check()
	if got, body := last(""); got == nil || got.Status != "failed" || got.Reason != "secret_refused" ||
		strings.Contains(body, secret) || strings.Contains(body, mcp.URL+"/mcp\",\"") {
		t.Fatalf("after a refused check, last_check = %+v, want failed secret_refused", got)
	}

	hub.mustJSON("DELETE", "/api/loops/aster/connections/docs", nil, nil)
	hub.mustJSON("POST", "/api/connections/docs/revoke", nil, nil)
	if got, _ := last(""); got != nil {
		t.Errorf("after a revoke, last_check = %+v, want none", got)
	}
}
