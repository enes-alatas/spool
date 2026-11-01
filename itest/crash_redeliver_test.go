//go:build integration

package itest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	// the control room reads which loop dropped it, and only it (#1205)
	doomedID := hub.loop("doomed").ID
	for text, dropped := range map[string]bool{"the poisoned question": true, "the late question": false} {
		stored := hub.activityWith(text)
		if len(stored) != 1 || slices.Contains(stored[0].DroppedBy, doomedID) != dropped {
			t.Errorf("%q stored as %+v, want dropped by the loop %t", text, stored, dropped)
		}
	}

	setScript(t, workspace, "!echo\n")
	answered := hub.waitTurn("doomed", 90*time.Second, func(done turn) bool {
		return !done.IsError && strings.Contains(done.ResultText, "the late question")
	})
	if strings.Contains(answered.ResultText, "the poisoned question") {
		t.Fatalf("the dropped message was said again:\n%s", answered.ResultText)
	}
}

// TestADroppedMessageIsToldToItsSender: a person whose message a loop
// dropped is told so once, by a hub notice that replies to their message
// in the chat they wrote it in, on the loop's own bot (#1205). The notice
// is not a message, and the control room reads the drop on the row.
func TestADroppedMessageIsToldToItsSender(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	workspace := t.TempDir()
	hub, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspace, "workspace_mode": "dir"})
	hub.waitTurn("alpha", firstTurnWait, func(turn) bool { return true })
	hub.waitState("alpha", "asleep", 30*time.Second)
	setScript(t, workspace, "!crash\n")

	const text = "@alpha the poisoned question"
	posted := tg.post(groupChatID, "supergroup", text, operator)
	if !hub.hasEvent("alpha", "batch_dropped", 2*time.Minute) {
		t.Fatalf("nothing was dropped; turns: %s", dump(hub.turns("alpha")))
	}
	const noticeText = "couldn't take this message"
	notice := tg.waitSent(t, groupChatID, noticeText)
	if notice.Token != "alpha" || notice.ReplyTo != posted.ids["alpha"] {
		t.Fatalf("notice sent by %q replying to %d, want alpha's bot replying to its %d", notice.Token,
			notice.ReplyTo, posted.ids["alpha"])
	}
	if !strings.Contains(notice.Text, "Spool: alpha") {
		t.Fatalf("the notice does not name the hub and the loop: %q", notice.Text)
	}
	// give a second notice the time to go out, if it were going to
	time.Sleep(2 * time.Second)
	if told := countSent(tg, groupChatID, noticeText); told != 1 {
		t.Fatalf("the sender was told of one drop %d times, want once", told)
	}
	if !hub.hasEvent("alpha", "owner_notice", 5*time.Second) {
		t.Fatal("the drop notice went out without an owner_notice on the timeline")
	}
	for _, stored := range hub.activity() {
		if strings.Contains(stored.Text, noticeText) {
			t.Fatalf("the hub's notice was recorded as a message: %s", dump(stored))
		}
	}
	if stored := hub.activityWith(text); len(stored) != 1 || !slices.Contains(stored[0].DroppedBy, hub.loop("alpha").ID) {
		t.Fatalf("the dropped message is stored as %s, want it dropped by alpha", dump(stored))
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
