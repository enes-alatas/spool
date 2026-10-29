//go:build integration

package itest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime/cloud"
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
	cloudToken, containedToken := cloudTokenOf("cirrus"), hubMCPToken(t, s, "aster")

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
	if got := postTo(t, s.cloudURL, cloudTokenOf("cirrus"), oversized); got < 400 {
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

// makeCloudLoop makes the loop name, in the stopped hub at dataDir, a
// cloud loop set up as ADR-0061's setup leaves one: its repo
// example-org/<name> on main, which it makes on the fake GitHub, its own
// environment, cloudEnvironmentOf, and its hub token, cloudTokenOf, kept
// as its hash. The API creates a cloud loop only once Settings → Cloud is
// green, so the store is told directly. setUpCloudEnvironment writes the
// environment's side once the hub's cloud listener is up.
func makeCloudLoop(t *testing.T, dataDir, name string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sum := sha256.Sum256([]byte(cloudTokenOf(name)))
	if _, err := db.Exec(`UPDATE loops SET runtime = 'cloud', cloud_repo = ?, cloud_branch = 'main', cloud_environment_id = ?,
		hub_mcp_token = '', hub_mcp_token_hash = ? WHERE name = ?`,
		"example-org/"+name, cloudEnvironmentOf(name), hex.EncodeToString(sum[:]), name); err != nil {
		t.Fatalf("make %s a cloud loop: %v", name, err)
	}
	makeGitHubRepo(t, filepath.Join(dataDir, "fkstate"), "example-org/"+name)
}

// cloudEnvironmentOf is the environment the harness's cloud loop name
// starts in, one of its own.
func cloudEnvironmentOf(name string) string { return "env_01Fixture" + name }

// cloudTokenOf is the synthetic hub token the harness's cloud loop name
// holds until Rotate token mints another.
func cloudTokenOf(name string) string { return "spool-itest-synthetic-" + name }

// makeGitHubRepo makes repo, owner/name, on fakeclaude's GitHub under
// fkState: a bare repo with one commit on main, holding a CLAUDE.md.
func makeGitHubRepo(t *testing.T, fkState, repo string) {
	t.Helper()
	work := t.TempDir()
	bare := filepath.Join(fkState, "github", repo)
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=itest", "GIT_AUTHOR_EMAIL=itest@example.test", "GIT_COMMITTER_NAME=itest", "GIT_COMMITTER_EMAIL=itest@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	git(bare, "init", "-q", "--bare", "-b", "main")
	git(work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "CLAUDE.md"), []byte("The repo "+repo+".\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "CLAUDE.md")
	git(work, "commit", "-q", "-m", "init")
	git(work, "push", "-q", bare, "main")
}

// setUpCloudEnvironment writes environment as the operator would set it on
// claude.ai: the hub's setup script for the listener's address, and token
// as its network secret for the listener's host.
func setUpCloudEnvironment(t *testing.T, hub *server, environment, token string) {
	t.Helper()
	script, err := cloud.SetupScript(hub.cloudURL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(hub.fkState, "environments", environment)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(strings.TrimPrefix(hub.cloudURL, "http://"), "https://")
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte(host+" "+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
