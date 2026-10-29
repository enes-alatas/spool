//go:build integration

package itest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
// --cloud` on a terminal, from a start directory: an empty repo whose
// origin is the loop's repo on GitHub, with --ref naming its branch and
// --settings its environment, and nothing bundled (ADR-0061). The session
// runs the environment's setup script, whose SessionStart hook tells the
// hub the session's id and adds the hub's prompt to its context. Each turn
// comes back by the Stop hook, and the next wake sends into the same
// session with `claude -p --cloud`. The token never enters the VM: the
// egress proxy adds it as the environment's secret.
func TestCloudCLILoopStartsFromItsRepoAndReadsItsTurnsBack(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "cirrus")

	s.message("cirrus", "first words")
	first := s.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if first.IsError || !strings.HasPrefix(first.ResultText, "fake cloud reply: ") {
		t.Fatalf("the first turn: %+v", first)
	}
	vms := cloudVMs(t, s)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	vm := filepath.Join(s.fkState, "cloud", vms[0])
	var viewed cloudLoopJSON
	s.mustJSON("GET", "/api/loops/cirrus", nil, &viewed)
	if viewed.CloudSessionURL != "https://claude.ai/code/"+vms[0] {
		t.Errorf("the loop's session link = %q, want the session its SessionStart posted", viewed.CloudSessionURL)
	}
	args, _ := os.ReadFile(filepath.Join(vm, "cli-args"))
	var denied []string
	for _, tool := range []string{"list_sessions", "get_session", "list_events", "list_triggers", "list_environments", "send_later",
		"send_message", "create_session", "archive_session", "create_trigger", "fire_trigger", "update_trigger", "delete_trigger", "check_repo_access"} {
		denied = append(denied, `"mcp__claude-code-remote__`+tool+`"`)
	}
	wantArgs := "first words\n--permission-mode\nauto\n--settings\n" +
		`{"permissions":{"deny":[` + strings.Join(denied, ",") + `]},"remote":{"defaultEnvironmentId":"` + cloudEnvironmentOf("cirrus") + `"}}` + "\n--ref\nmain\n"
	if !strings.HasPrefix(string(args), "--cloud\n") || !strings.HasSuffix(string(args), wantArgs) {
		t.Errorf("the start ran with\n%s\nwant --cloud, the wake, and then\n%s", args, wantArgs)
	}
	if repo, err := os.ReadFile(filepath.Join(vm, "repo", "CLAUDE.md")); err != nil || string(repo) != "The repo example-org/cirrus.\n" {
		t.Errorf("the session's repo holds %q (%v), want a clone of example-org/cirrus", repo, err)
	}
	if context, err := os.ReadFile(filepath.Join(vm, "context")); err != nil || !strings.Contains(string(context), "cirrus") {
		t.Errorf("the session's context is %q (%v), want the hub's prompt", context, err)
	}
	if vmHolds(t, s, vms[0], cloudTokenOf("cirrus")) {
		t.Error("the VM holds the loop's token")
	}
	if _, err := os.Stat(filepath.Join(vm, "local-session-starts")); err == nil {
		t.Error("the local CLI fired a SessionStart hook: the start directory holds none")
	}
	if strings.Contains(s.log(), "cloud hook refused") {
		t.Errorf("the hub refused a hook post:\n%s", s.log())
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
	starts, _ := filepath.Glob(filepath.Join(hubCLIDir(s.dataDir), "spool-start-*"))
	if len(starts) != 0 {
		t.Errorf("start directories outlived their starts: %v", starts)
	}
}

// A session that compacts loses the context SessionStart gave it. Its
// post from the loop's session, during a turn, is answered with the
// prompt alone: the hook adds it as context again and leaves the env
// file as the first start wrote it (ADR-0061).
func TestACompactedCloudSessionIsHandedItsPromptAgain(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "nimbus")
	if resp, body := hub.do("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "synthetic-gh-value",
	}); resp.StatusCode != 201 {
		t.Fatalf("creating the connection: %d %s", resp.StatusCode, body)
	}
	if resp, body := hub.do("PUT", "/api/loops/nimbus/connections/github", nil); resp.StatusCode != 204 {
		t.Fatalf("attaching the connection: %d %s", resp.StatusCode, body)
	}
	hub.message("nimbus", "first words")
	hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	vm := filepath.Join(hub.fkState, "cloud", vms[0])
	prompt, err := os.ReadFile(filepath.Join(vm, "context"))
	if err != nil || !strings.Contains(string(prompt), "nimbus") {
		t.Fatalf("the session's first context is %q (%v), want the hub's prompt", prompt, err)
	}
	env, err := os.ReadFile(filepath.Join(vm, "claude-env"))
	if err != nil || len(env) == 0 {
		t.Fatalf("the session's env file is %q (%v), want the loop's connections", env, err)
	}

	hub.waitState("nimbus", "asleep", 20*time.Second)
	hub.message("nimbus", "!compact\nsecond words")
	compacted := hub.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if compacted.IsError {
		t.Fatalf("the compacted turn: %+v", compacted)
	}
	if context, _ := os.ReadFile(filepath.Join(vm, "context")); string(context) != string(prompt)+string(prompt) {
		t.Errorf("the session's context after compacting is\n%s\nwant the prompt twice", context)
	}
	if again, _ := os.ReadFile(filepath.Join(vm, "claude-env")); string(again) != string(env) {
		t.Errorf("compacting changed the session's env file:\n%s\nwant\n%s", again, env)
	}
	if !strings.Contains(hub.log(), "a session was handed its loop's prompt again") {
		t.Errorf("the compaction's post wasn't logged as answered:\n%s", hub.log())
	}
	if strings.Contains(hub.log(), "cloud hook refused") {
		t.Errorf("the hub refused a hook post:\n%s", hub.log())
	}
}

// hubCLIDir is where the hub at dataDir makes its cloud CLI runs, as
// cmd/spool names it.
func hubCLIDir(dataDir string) string {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join(os.TempDir(), "spool-cloud-"+hex.EncodeToString(sum[:6]))
}

// CLI 2.1.310 ends `--cloud` once it has created the session, before the
// VM posts SessionStart (#1102). The start waits on for that post: the
// loop records its session, and its next wake sends into it.
func TestACloudStartWhoseCLIExitsFirstKeepsItsSession(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "altus")
	if err := os.WriteFile(filepath.Join(hub.fkState, "cloud-start-exits"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	hub.message("altus", "first words")
	first := hub.waitTurn("altus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if first.IsError {
		t.Fatalf("the first turn: %+v, want the session's reply", first)
	}
	hub.waitState("altus", "asleep", 20*time.Second)
	hub.message("altus", "second words")
	second := hub.waitTurn("altus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "second words") })
	if second.IsError || second.SessionID != first.SessionID {
		t.Fatalf("the second wake's turn: %+v, want one in session %s", second, first.SessionID)
	}
	if vms := cloudVMs(t, hub); len(vms) != 1 {
		t.Errorf("two wakes made %d sessions, want the second to send into the first", len(vms))
	}
	if strings.Contains(hub.log(), "cloud hook refused") {
		t.Errorf("the hub refused a hook post:\n%s", hub.log())
	}
}

// The hook route takes a cloud loop's SessionStart only with a session id
// of a session's shape, and only while a start of that loop is pending: a
// post from the hub's own machine carries none (#957, run E3), and the
// loop itself can post with its token at any time.
func TestCloudHookRouteRefusesASessionStartItIsNotWaitingFor(t *testing.T) {
	t.Parallel()
	s := startCloudHub(t, "nimbus")
	// once a turn has ended, no start of the loop is pending
	s.message("nimbus", "hello")
	s.waitTurn("nimbus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "hello") })
	token := cloudTokenOf("nimbus")
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
