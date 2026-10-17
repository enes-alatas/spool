//go:build integration

package itest

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// The cloud listener (ADR-0056) is off unless the operator turns it on: a
// hub started without --cloud-listen serves nothing to the internet.
func TestCloudListenerIsOffByDefault(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	if s.cloudURL != "" {
		t.Fatalf("a hub started without --cloud-listen serves the cloud listener on %s", s.cloudURL)
	}
	if !strings.Contains(s.log(), `cloud_addr=""`) {
		t.Fatalf("the listening line does not say the cloud listener is off; log:\n%s", s.log())
	}
}

// The cloud listener admits a cloud loop's token and no other: a contained
// loop's token is refused there exactly as an unknown one is, so a token
// that leaks out of a workstation is useless from the internet. Every
// path but /mcp and the address check is 404, the brokered servers' too.
func TestCloudListenerAdmitsCloudLoopsOnly(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("cirrus", nil)
	s.createLoop("aster", nil)
	s.stop()
	makeCloudLoop(t, dataDir, "cirrus")

	s = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-listen", "0")
	if !strings.HasPrefix(s.cloudURL, "http://127.0.0.1:") {
		t.Fatalf("--cloud-listen with a port alone serves on %q, want loopback", s.cloudURL)
	}
	cloudToken, containedToken := hubMCPToken(t, s, "cirrus"), hubMCPToken(t, s, "aster")

	sess := mcpSessionAt(t, s.cloudURL, cloudToken)
	res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "hello from the cloud"})
	if res.IsError {
		t.Fatalf("a cloud loop's send on the cloud listener was refused: %s", resultText(res))
	}
	if len(s.activityWith("hello from the cloud")) == 0 {
		t.Fatal("the cloud loop's send was answered but never stored")
	}

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"0"}}}`
	if got := postTo(t, s.cloudURL, containedToken, initialize); got != http.StatusUnauthorized {
		t.Errorf("a contained loop's token on the cloud listener: %d, want 401", got)
	}
	if got := postTo(t, s.cloudURL, "", initialize); got != http.StatusUnauthorized {
		t.Errorf("no token on the cloud listener: %d, want 401", got)
	}
	// The loop listener is unchanged: the contained loop's token works there.
	if got := postTo(t, s.mcpURL, containedToken, initialize); got != http.StatusOK {
		t.Errorf("a contained loop's token on the loop listener: %d, want 200", got)
	}

	for _, path := range []string{
		"/mcp/connections/github/", "/api/loops", "/api/version", "/", "/mcp/check/never-issued",
	} {
		resp, err := http.Get(s.cloudURL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s on the cloud listener: %d, want 404", path, resp.StatusCode)
		}
	}
}

// Anyone on the internet reaches the cloud listener's parser, so its size
// bounds apply before a token is read: an oversized header is refused
// without one, and an oversized body is refused even with a cloud loop's.
func TestCloudListenerBoundsComeBeforeAuth(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("cirrus", nil)
	s.stop()
	makeCloudLoop(t, dataDir, "cirrus")
	s = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-listen", "0")

	req, _ := http.NewRequest(http.MethodPost, s.cloudURL+"/mcp", strings.NewReader("{}"))
	req.Header.Set("X-Padding", strings.Repeat("a", 64<<10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("a 64 KiB header with no token: %d, want 431", resp.StatusCode)
	}

	oversized := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"pad":"` + strings.Repeat("a", 2<<20) + `"}}`
	if got := postTo(t, s.cloudURL, hubMCPToken(t, s, "cirrus"), oversized); got < 400 {
		t.Errorf("a 2 MiB body with a cloud loop's token: %d, want it refused", got)
	}
}

// postTo posts one JSON-RPC body to a listener's /mcp, with token as its
// bearer unless it is empty, and returns the status.
func postTo(t *testing.T, baseURL, token, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// makeCloudLoop turns a stopped hub's loop into a cloud loop, which
// the API creates only once Settings → Cloud is green: the store is told
// directly, so a test needn't set the hub up first.
func makeCloudLoop(t *testing.T, dataDir, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE loops SET runtime = 'cloud' WHERE name = ?`, name); err != nil {
		t.Fatalf("make %s a cloud loop: %v", name, err)
	}
}
