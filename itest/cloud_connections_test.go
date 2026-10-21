//go:build integration

package itest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloud loop's env-var connections reach its session's shell as the
// session starts: the hub answers the loop home's SessionStart post with
// them, past the response redactor, and the hook writes them to
// CLAUDE_ENV_FILE. A VM Anthropic reclaimed keeps that file, and its
// resume posts nothing. A resume post is refused, which opens a problem on
// the loop, and the next start's answer clears it. A turn that prints the
// value comes back redacted (ADR-0059).
func TestACloudLoopsShellGetsItsConnections(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	// synthetic, and shaped to break a line the hub quoted carelessly
	const value = "synthetic-gh it's $(touch pwned) `touch pwned` \"quoted\""
	sum := sha256.Sum256([]byte(value))
	want := hex.EncodeToString(sum[:])
	if resp, body := hub.do("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": value,
	}); resp.StatusCode != 201 {
		t.Fatalf("creating the connection: %d %s", resp.StatusCode, body)
	}
	if resp, body := hub.do("PUT", "/api/loops/cirrus/connections/github", nil); resp.StatusCode != 204 {
		t.Fatalf("attaching the connection: %d %s", resp.StatusCode, body)
	}

	shellSees := func(words string) string {
		t.Helper()
		hub.waitState("cirrus", "asleep", 20*time.Second)
		before := len(hub.completed("cirrus"))
		hub.message("cirrus", words+"\n!envsum GH_TOKEN")
		var reply turn
		within(t, 30*time.Second, words+"'s turn", func() bool {
			completed := hub.completed("cirrus")
			if len(completed) <= before {
				return false
			}
			for _, listed := range completed {
				if listed.StartedAt >= reply.StartedAt {
					reply = listed
				}
			}
			return true
		})
		if reply.IsError || !strings.HasPrefix(reply.ResultText, "GH_TOKEN=") {
			t.Fatalf("%s: the turn %+v isn't the fake's answer", words, reply)
		}
		return strings.TrimPrefix(reply.ResultText, "GH_TOKEN=")
	}
	if got := shellSees("first start"); got != want {
		t.Fatalf("the session's shell sees GH_TOKEN as %q, want the connection's value as it is", got)
	}
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("the loop made %d sessions, want 1", len(vms))
	}
	vm := filepath.Join(hub.fkState, "cloud", vms[0])
	if _, err := os.Stat(filepath.Join(vm, "repo", "pwned")); err == nil {
		t.Error("reading the hand-off ran a command its value held")
	}

	if err := os.WriteFile(filepath.Join(vm, "reclaimed"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shellSees("after a reclaim"); got != want {
		t.Errorf("a reclaimed VM's shell sees GH_TOKEN as %q, want the value its env file kept", got)
	}
	if view := hub.loop("cirrus"); len(view.Problems) != 0 {
		t.Errorf("problems = %+v after a reclaim, want none: its resume posts nothing", view.Problems)
	}

	// a resume: a VM woken on claude.ai, or a lifted token
	token := hubMCPToken(t, hub, "cirrus")
	if status, body := postSessionStart(t, hub, token, vms[0], "resume"); status != http.StatusConflict || strings.Contains(body, "synthetic-gh") {
		t.Fatalf("a resume: %d %q, want 409 and no value", status, body)
	}
	view := hub.waitHealth("cirrus", "warning")
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "cloud_connections" {
		t.Fatalf("problems = %+v, want one cloud_connections", view.Problems)
	}
	// stopping and starting the loop hands them over again, to a new session
	hub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	hub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)
	if got := shellSees("handed over again"); got != want {
		t.Errorf("the next start's shell sees GH_TOKEN as %q, want the value", got)
	}
	if vms := cloudVMs(t, hub); len(vms) != 2 {
		t.Errorf("the loop made %d sessions, want a new one after the stop", len(vms))
	}
	hub.waitHealth("cirrus", "ok")

	// the value comes back redacted, as a contained loop's does
	hub.waitState("cirrus", "asleep", 20*time.Second)
	hub.message("cirrus", "!env GH_TOKEN")
	shown := hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.HasPrefix(turn.ResultText, "GH_TOKEN=<") })
	if strings.Contains(shown.ResultText, "synthetic-gh") {
		t.Errorf("the turn carried the value unredacted: %q", shown.ResultText)
	}
}

// postSessionStart posts a SessionStart from source for session to the
// hub's hook route with token as its bearer, as a loop home's hook does,
// and returns the status and the answer.
func postSessionStart(t *testing.T, hub *server, token, session, source string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("hook", `{"hook_event_name":"SessionStart","source":"`+source+`"}`)
	_ = form.WriteField("session", session)
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, hub.cloudURL+"/hooks", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(answer)
}
