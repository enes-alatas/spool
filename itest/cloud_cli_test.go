//go:build integration

package itest

import (
	"bytes"
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
// --cloud` on a terminal, in a loop home whose SessionStart hook tells the
// hub the session's id. Each turn comes back by the home's Stop hook, and
// the next wake sends into the same session with `claude -p --cloud`. The
// home's hooks did nothing when the local CLI fired SessionStart on the
// hub's own machine, where there is no remote session (#957, run E3).
func TestCloudCLILoopReadsItsTurnsBackFromItsHooks(t *testing.T) {
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
	homes, _ := filepath.Glob(filepath.Join(os.TempDir(), "spool-cloud-*", "spool-home-*", "CLAUDE.md"))
	for _, home := range homes {
		if prompt, _ := os.ReadFile(home); strings.Contains(string(prompt), "cirrus") {
			t.Errorf("a loop home outlived its start: %s", filepath.Dir(home))
		}
	}
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
