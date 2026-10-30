//go:build integration

package itest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestAMessageOutlivesAKilledProcess: a claude process killed from outside
// mid-turn (a workstation's docker daemon going away, in #1189) ends a turn
// nobody answered. The message that turn carried goes back to the queue and
// reaches the retry, rather than dying with the process. The operator's own
// kill still ends a turn for good: its message is not said again.
func TestAMessageOutlivesAKilledProcess(t *testing.T) {
	t.Parallel()
	workspace := workspaceWithScript(t, "!echo\n!hang 60\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("sturdy", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
	})
	hub.waitTurn("sturdy", firstTurnWait, func(turn) bool { return true })
	hub.waitState("sturdy", "asleep", 30*time.Second)

	hub.message("sturdy", "the question nobody answered")
	killed := hub.waitRunningTurn("sturdy", 30*time.Second, func(running turn) bool {
		return running.Trigger == "message"
	})
	// the retry resumes the same session, whose turn count the killed turn
	// never saved, so it reads the script's second line again: echo there
	setScript(t, workspace, "!echo\n")
	killClaude(t, hub, "sturdy")

	failed := hub.waitTurn("sturdy", 30*time.Second, func(done turn) bool {
		return done.ID == killed.ID
	})
	if !failed.IsError {
		t.Fatalf("the killed turn finished clean: %+v", failed)
	}
	retried := hub.waitTurn("sturdy", 60*time.Second, func(done turn) bool {
		return done.StartedAt > failed.StartedAt && !done.IsError
	})
	if !strings.Contains(retried.ResultText, "the question nobody answered") {
		t.Fatalf("the retry did not carry the killed turn's message:\n%s", retried.ResultText)
	}
	hub.waitState("sturdy", "asleep", 30*time.Second)

	// the operator's kill: the turn is ended on purpose, and nothing of it
	// comes back on the ladder's first rung
	setScript(t, workspace, "!hang 60\n")
	hub.message("sturdy", "never mind this one")
	ended := hub.waitRunningTurn("sturdy", 30*time.Second, func(running turn) bool {
		return running.Trigger == "message" && running.StartedAt > retried.StartedAt
	})
	hub.mustJSON("POST", "/api/loops/sturdy/kill", nil, nil)
	hub.waitTurn("sturdy", 30*time.Second, func(done turn) bool { return done.ID == ended.ID })
	hub.waitState("sturdy", "asleep", 30*time.Second)
	time.Sleep(13 * time.Second)
	for _, later := range hub.turns("sturdy") {
		if later.StartedAt > ended.StartedAt {
			t.Fatalf("the operator's kill was retried: %+v", later)
		}
	}
}

// TestAMessageThatKillsEveryProcessIsDropped: a message whose turn kills
// every process that runs it is tried three times and then dropped, with an
// event saying so, rather than held at the front of the queue for good. A
// message from the same chat that joined it during its retries counts its
// own attempts (#1189): it survives the drop and is answered, and the
// dropped one is not said again.
func TestAMessageThatKillsEveryProcessIsDropped(t *testing.T) {
	t.Parallel()
	workspace := workspaceWithScript(t, "!echo\n!crash\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("doomed", map[string]any{
		"workspace_path": workspace,
		"workspace_mode": "dir",
	})
	// the creation tick saves the session, so every crash after it resumes
	// a session that loads and dies on the turn
	hub.waitTurn("doomed", firstTurnWait, func(turn) bool { return true })
	hub.waitState("doomed", "asleep", 30*time.Second)

	hub.message("doomed", "the poisoned question")
	// two attempts die, and the third waits on the ladder's 20s rung
	for deadline := time.Now().Add(60 * time.Second); attemptsAt(hub, "doomed", "the poisoned question") < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("the message was not tried twice; turns: %s", dump(hub.turns("doomed")))
		}
		time.Sleep(150 * time.Millisecond)
	}
	hub.message("doomed", "the late question")
	if !hub.hasEvent("doomed", "batch_dropped", 2*time.Minute) {
		t.Fatalf("nothing was dropped; turns: %s", dump(hub.turns("doomed")))
	}
	if tried := attemptsAt(hub, "doomed", "the poisoned question"); tried != 3 {
		t.Errorf("the message was tried %d times before it was dropped, want 3", tried)
	}
	if tried := attemptsAt(hub, "doomed", "the late question"); tried != 1 {
		t.Errorf("the late message was tried %d times by the drop, want the one it joined", tried)
	}

	setScript(t, workspace, "!echo\n")
	answered := hub.waitTurn("doomed", 90*time.Second, func(done turn) bool {
		return !done.IsError && strings.Contains(done.ResultText, "the late question")
	})
	if strings.Contains(answered.ResultText, "the poisoned question") {
		t.Fatalf("the dropped message was said again:\n%s", answered.ResultText)
	}
}

// attemptsAt counts a loop's errored turns that carried text.
func attemptsAt(hub *server, name, text string) int {
	inputs := hub.turnInputs(name)
	attempts := 0
	for _, done := range hub.completed(name) {
		if done.IsError && strings.Contains(strings.Join(inputs[done.ID], "\n"), text) {
			attempts++
		}
	}
	return attempts
}

// setScript replaces a bare loop's turn script; the next spawn reads it.
func setScript(t *testing.T, workspace, lines string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, ".fakeclaude"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

// killClaude SIGKILLs a bare loop's newest claude process from outside the
// hub, the way a dying workstation or the OOM killer ends one.
func killClaude(t *testing.T, hub *server, name string) {
	t.Helper()
	var newest spoolEvent
	for _, event := range hub.eventsQuery(name, "limit=200") {
		if event.Subtype == "proc_spawn" && event.ID > newest.ID {
			newest = event
		}
	}
	var spawn struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(newest.Payload), &spawn); err != nil || spawn.PID == 0 {
		t.Fatalf("no claude process to kill: %q (%v)", newest.Payload, err)
	}
	if err := syscall.Kill(spawn.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}
