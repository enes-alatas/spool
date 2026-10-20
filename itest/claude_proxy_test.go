//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// startProxyHub starts a bare hub with one loop, its Claude login signed in
// on the synthetic token and Anthropic's API a stand-in that admits that
// token alone.
func startProxyHub(t *testing.T, name string) (*server, *anthropicFake) {
	t.Helper()
	fake := newAnthropicFake(t, cloudFixtureToken)
	dataDir := t.TempDir()
	s := startServerArgs(t, dataDir, "--runtime", "bare")
	s.createLoop(name, nil)
	s.stop()
	storeCloudAccount(t, dataDir, store.CloudAccount{
		Token:        cloudFixtureToken,
		RefreshToken: "sk-ant-ort01-fixture-cloud-refresh",
		ExpiresAt:    time.Now().Add(8 * time.Hour).UnixMilli(),
		OrgUUID:      "00000000-0000-4000-8000-000000000001",
	})
	return startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-api-base", fake.URL), fake
}

// postModel sends a model request to the Claude proxy as claude does,
// logged in with token, and returns the status.
func postModel(t *testing.T, s *server, method, path, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, s.mcpURL+path, bytes.NewBufferString(`{"model":"claude-haiku-fixture","max_tokens":1,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Api-Key", "sk-ant-api03-fixture-not-forwarded")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The Claude proxy on the loop listener forwards a loop's model request to
// Anthropic's API with the hub's Claude token in place of the loop's own
// hub token, which is the only credential it admits. It forwards model
// requests alone: the sessions API, which the same token drives, is
// refused, and nothing else of the request's credentials crosses
// (ADR-0058).
func TestTheClaudeProxyForwardsModelRequestsOnTheHubsToken(t *testing.T) {
	t.Parallel()
	s, fake := startProxyHub(t, "aster")
	token := hubMCPToken(t, s, "aster")

	if got := postModel(t, s, "POST", "/claude/v1/messages?beta=true", token); got != http.StatusOK {
		t.Fatalf("a loop's model request answered %d, want 200 from the stand-in on the hub's token", got)
	}
	fake.Mu.Lock()
	messages := append([]apiMessage(nil), fake.Messages...)
	fake.Mu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("the stand-in took %d model requests, want 1", len(messages))
	}
	if got := messages[0].Headers.Get("Authorization"); got != "Bearer "+cloudFixtureToken {
		t.Errorf("forwarded Authorization = %q, want the hub's Claude token", got)
	}
	if got := messages[0].Headers.Get("X-Api-Key"); got != "" {
		t.Errorf("the loop's own x-api-key crossed: %q", got)
	}
	if messages[0].Query != "beta=true" || messages[0].Body["model"] != "claude-haiku-fixture" {
		t.Errorf("forwarded query %q and body %v, want the loop's own", messages[0].Query, messages[0].Body)
	}

	for _, refused := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", "/claude/v1/messages", "", http.StatusUnauthorized},
		{"POST", "/claude/v1/messages", "not-a-loop-token", http.StatusUnauthorized},
		{"POST", "/claude/v1/messages", cloudFixtureToken, http.StatusUnauthorized},
		{"GET", "/claude/v1/messages", token, http.StatusNotFound},
		{"POST", "/claude/v1/sessions", token, http.StatusNotFound},
		{"GET", "/claude/v1/environment_providers", token, http.StatusNotFound},
		{"POST", "/claude/v1/messages/../sessions", token, http.StatusNotFound},
	} {
		if got := postModel(t, s, refused.method, refused.path, refused.token); got != refused.want {
			t.Errorf("%s %s with token %q answered %d, want %d", refused.method, refused.path, refused.token, got, refused.want)
		}
	}
	fake.Mu.Lock()
	reached := len(fake.Messages)
	fake.Mu.Unlock()
	if reached != 1 {
		t.Errorf("a refused request reached the stand-in: %d model requests", reached)
	}
}

// With no Claude login and no setup-token the proxy refuses a model request
// the way Anthropic's API refuses a bad login, which claude reports as a
// refused login.
func TestTheClaudeProxyRefusesWithNoLogin(t *testing.T) {
	t.Parallel()
	s, fake := startProxyHub(t, "cosmos")
	s.mustJSON("DELETE", "/api/settings/cloud/account", nil, nil)
	if got := postModel(t, s, "POST", "/claude/v1/messages", hubMCPToken(t, s, "cosmos")); got != http.StatusUnauthorized {
		t.Fatalf("with no login a model request answered %d, want 401", got)
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.Messages) != 0 {
		t.Fatal("a model request reached the API with no login")
	}
}

// dockerLoopReachesTheModelThroughTheClaudeProxy: a contained loop holds no
// Claude credential. Its claude gets the Claude proxy as its base URL and
// its own hub token as its login, and is told to make no call past the
// proxy. Its model request reaches Anthropic's API through the hub on the
// hub's Claude token (ADR-0058).
func dockerLoopReachesTheModelThroughTheClaudeProxy(t *testing.T) {
	fake := newAnthropicFake(t, dockerTestToken)
	s := startDockerServer(t, t.TempDir(), "--cloud-api-base", fake.URL)
	s.createLoop("wsproxy", nil)
	cleanupWorkstation(t, s.loop("wsproxy").ID)

	// one probe per script: fakeclaude answers a session's turn N with line
	// N, and the creation tick may already have taken turn 1, so a single
	// line, which repeats, is the only one every turn reads
	probe := func(directive, ask, prefix string) turn {
		s.scriptLoop("wsproxy", directive+"\n")
		s.message("wsproxy", ask)
		return s.waitTurn("wsproxy", 90*time.Second, func(tr turn) bool { return strings.HasPrefix(tr.ResultText, prefix) })
	}
	login := probe("!env64 CLAUDE_CODE_OAUTH_TOKEN", "which login?", "CLAUDE_CODE_OAUTH_TOKEN=")
	base := probe("!env ANTHROPIC_BASE_URL", "where?", "ANTHROPIC_BASE_URL=")
	quiet := probe("!env CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "anything past the proxy?", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=")
	asked := probe("!model", "ask the model", "model:")

	// the redactor names what it matched: the loop's hub token, never the
	// hub's Claude token
	if login.ResultText != "CLAUDE_CODE_OAUTH_TOKEN=<redacted:hub_mcp_token>" {
		t.Errorf("the contained loop's login = %q, want its own hub token", login.ResultText)
	}
	if !strings.HasSuffix(base.ResultText, ":"+s.port(s.mcpURL)+"/claude") {
		t.Errorf("the contained loop's base URL = %q, want the Claude proxy on the loop listener", base.ResultText)
	}
	// claude's direct calls to api.anthropic.com would carry the hub token
	if quiet.ResultText != "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1" {
		t.Errorf("the contained loop's claude is not kept to the proxy: %q", quiet.ResultText)
	}
	if asked.ResultText != "model: 200" {
		t.Fatalf("the contained loop's model request answered %q, want 200 through the proxy", asked.ResultText)
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	if len(fake.Messages) != 1 || fake.Messages[0].Headers.Get("Authorization") != "Bearer "+dockerTestToken {
		t.Fatalf("the stand-in took %d model requests, want 1 on the hub's token", len(fake.Messages))
	}
}

// storeCloudAccount stores account as the signed-in one, with the hub
// stopped.
func storeCloudAccount(t *testing.T, dataDir string, account store.CloudAccount) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Settings().Set(context.Background(), store.SettingCloudAccount, string(raw)); err != nil {
		t.Fatal(err)
	}
}
