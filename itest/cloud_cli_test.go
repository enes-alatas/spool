//go:build integration

package itest

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// startCloudCLIHub is startCloudHub with cloud loops driven through the
// claude CLI, fakeclaude's --cloud, under the hub's temporary flag
// (ADR-0059, until #977).
func startCloudCLIHub(t *testing.T, name string) *server {
	t.Helper()
	s, _ := startCloudHub(t, name, "--temporary-cloud-cli")
	return s
}

// cloudVMs lists the sessions fakeclaude's --cloud started, by id.
func cloudVMs(t *testing.T, s *server) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.fkState, "cloud"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.Name())
	}
	return ids
}

// A cloud loop driven through the CLI starts its session with `claude
// --cloud` on a terminal, in a loop home whose SessionStart hook tells the
// hub the session's id. Each turn comes back by the home's Stop hook, and
// the next wake sends into the same session with `claude -p --cloud`. The
// home's hooks did nothing when the local CLI fired SessionStart on the
// hub's own machine, where there is no remote session (#957, run E3).
func TestCloudCLILoopReadsItsTurnsBackFromItsHooks(t *testing.T) {
	t.Parallel()
	s := startCloudCLIHub(t, "cirrus")

	s.message("cirrus", "first words")
	first := s.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if first.IsError || !strings.HasPrefix(first.ResultText, "fake cloud reply: ") {
		t.Fatalf("the first turn: %+v", first)
	}
	vms := cloudVMs(t, s)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/cirrus", nil, &viewed)
	if viewed.CloudSessionURL != "https://claude.ai/code/"+vms[0] {
		t.Errorf("the loop's session link = %q, want the session its SessionStart posted", viewed.CloudSessionURL)
	}
	local, err := os.ReadFile(filepath.Join(s.fkState, "cloud", vms[0], "local-session-starts"))
	if err != nil || strings.Count(string(local), "exit=<nil>") != 1 || strings.Contains(string(local), `output=""`) == false {
		t.Errorf("the local SessionStart hook ran as %q (%v), want once, quiet and clean", local, err)
	}
	if strings.Contains(s.log(), "cloud hook refused") {
		t.Errorf("the hub refused a hook post, the local one's among them:\n%s", s.log())
	}

	s.waitState("cirrus", "asleep", 20*time.Second)
	s.message("cirrus", "second words")
	second := s.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID != first.SessionID {
		t.Fatalf("the second wake's turn: %+v, want one in session %s", second, first.SessionID)
	}
	if vms := cloudVMs(t, s); len(vms) != 1 {
		t.Errorf("a second wake made %d sessions, want it to send into the one", len(vms))
	}
	homes, _ := filepath.Glob(filepath.Join(os.TempDir(), "spool-home-*", "CLAUDE.md"))
	for _, home := range homes {
		if prompt, _ := os.ReadFile(home); strings.Contains(string(prompt), "cirrus") {
			t.Errorf("a loop home outlived its start: %s", filepath.Dir(home))
		}
	}
}

// The hook route takes a cloud loop's SessionStart only with a session id
// of a session's shape, and only while a start of that loop is pending: a
// post from the hub's own machine carries none (#957, run E3), and the
// loop itself can post with its token at any time.
func TestCloudHookRouteRefusesASessionStartItIsNotWaitingFor(t *testing.T) {
	t.Parallel()
	s := startCloudCLIHub(t, "nimbus")
	// once a turn has ended, no start of the loop is pending
	s.message("nimbus", "hello")
	s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := hubMCPToken(t, s, "nimbus")
	if got := postHook(t, s, token, ""); got != http.StatusBadRequest {
		t.Errorf("a SessionStart with no session id: %d, want 400", got)
	}
	if got := postHook(t, s, token, "session_01Synthetic"); got != http.StatusConflict {
		t.Errorf("a SessionStart with no start pending: %d, want 409", got)
	}
}

// postHook posts a SessionStart for session to the hub's hook route, with
// token as its bearer, as a loop home's hook does, and returns the status.
func postHook(t *testing.T, s *server, token, session string) int {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("hook", `{"hook_event_name":"SessionStart","source":"startup"}`)
	_ = form.WriteField("session", session)
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, s.cloudURL+"/hooks", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// mcpInitialize is a JSON-RPC initialize, the first call a session's MCP
// client makes.
const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"itest","version":"0"}}}`

// vmToken is the hub token the session's VM holds, as its loop home's
// .mcp.json carried it there: what a copy lifted out of that VM would be.
func vmToken(t *testing.T, s *server, vm string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.fkState, "cloud", vm, "repo", ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	token, _ := strings.CutPrefix(config.MCPServers["spool"].Headers["Authorization"], "Bearer ")
	return token
}

// Each session a cloud loop starts gets a hub token of its own, which
// revokes the one before (ADR-0059): an old VM of the loop, or a copy
// lifted out of it, can't bind itself to the loop's next start. Here a
// rotation's next start holds its VM's SessionStart back, and the old
// VM's token posts one while the start is pending: it is refused, and
// the start takes its own VM's post.
func TestAStaleVMOfALoopCantBindItsNextStart(t *testing.T) {
	t.Parallel()
	s := startCloudCLIHub(t, "nimbus")
	s.message("nimbus", "first words")
	first := s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	firstVM := cloudVMs(t, s)[0]
	stale := vmToken(t, s, firstVM)
	if stale == "" || stale != hubMCPToken(t, s, "nimbus") {
		t.Fatal("the first VM doesn't hold the loop's token")
	}

	held := filepath.Join(s.fkState, "cloud-start-held")
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.mustJSON("POST", "/api/loops/nimbus/rotate", nil, nil)
	within(t, 30*time.Second, "the rotation", func() bool { return strings.Contains(s.log(), "context rotated") })
	s.message("nimbus", "second words")
	within(t, 30*time.Second, "the next start's VM", func() bool { return len(cloudVMs(t, s)) == 2 })
	if got := postHook(t, s, stale, "session_01Stale"); got != http.StatusUnauthorized {
		t.Errorf("the old VM's SessionStart during the new start: %d, want 401", got)
	}
	if got := postTo(t, s.cloudURL, stale, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the old VM's token on /mcp: %d, want 401", got)
	}
	if err := os.Remove(held); err != nil {
		t.Fatal(err)
	}

	second := s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID == first.SessionID {
		t.Fatalf("the turn after the rotation: %+v, want one in a new session", second)
	}
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/nimbus", nil, &viewed)
	if strings.HasSuffix(viewed.CloudSessionURL, "session_01Stale") {
		t.Errorf("the stale post bound the loop to %s", viewed.CloudSessionURL)
	}
	if fresh := hubMCPToken(t, s, "nimbus"); fresh == stale || fresh != vmToken(t, s, newestVM(t, s, firstVM)) {
		t.Error("the new session doesn't hold a token of its own")
	}
}

// newestVM is the session's VM other than old, of the two there are.
func newestVM(t *testing.T, s *server, old string) string {
	t.Helper()
	for _, vm := range cloudVMs(t, s) {
		if vm != old {
			return vm
		}
	}
	t.Fatal("no VM besides the first")
	return ""
}

// Stopping a cloud loop, and killing it, revoke its session's token at
// once, without waiting for a turn (ADR-0059): an operator stopping a
// misbehaving loop cuts it off now. Its next wake starts a session of its
// own, on a token of its own.
func TestStoppingACloudLoopRevokesItsTokenAtOnce(t *testing.T) {
	t.Parallel()
	s := startCloudCLIHub(t, "stratus")
	s.message("stratus", "hello")
	first := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := hubMCPToken(t, s, "stratus")
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusOK {
		t.Fatalf("the loop's token before the stop: %d, want 200", got)
	}

	s.mustJSON("POST", "/api/loops/stratus/pause", nil, nil)
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusUnauthorized {
		t.Errorf("the session's token after the stop: %d, want 401", got)
	}
	if got := sessionEndReason(t, s, "stratus", first.SessionID); got != store.EndReasonPaused {
		t.Errorf("the stopped session ended as %q, want %q", got, store.EndReasonPaused)
	}
	s.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	s.message("stratus", "again")
	again := s.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "again") })
	if again.IsError || again.SessionID == first.SessionID || len(cloudVMs(t, s)) != 2 {
		t.Fatalf("the wake after the stop: %+v in %d VMs, want a new session", again, len(cloudVMs(t, s)))
	}
	token = hubMCPToken(t, s, "stratus")
	if got := postTo(t, s.cloudURL, token, mcpInitialize); got != http.StatusOK {
		t.Fatalf("the new session's token: %d, want 200", got)
	}

	s.mustJSON("POST", "/api/loops/stratus/kill", nil, nil)
	within(t, 10*time.Second, "the kill's revoke", func() bool {
		return postTo(t, s.cloudURL, token, mcpInitialize) == http.StatusUnauthorized
	})
}
