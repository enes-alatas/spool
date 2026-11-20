//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestABackgroundCommandsExitWakesTheLoop: a command the loop leaves
// running in the background outlives the turn that started it, though the
// loop's idle timeout is far shorter, and its exit wakes the loop in a turn
// the CLI starts itself, which the hub records as a background turn
// (#1709). With nothing left running, the process closes as before.
func TestABackgroundCommandsExitWakesTheLoop(t *testing.T) {
	t.Parallel()
	workspace := workspaceWithScript(t, "!bg 5 started a watch\n!echo\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("watcher", map[string]any{
		"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
	})
	first := hub.waitTurn("watcher", firstTurnWait, func(turn) bool { return true })

	woken := hub.waitTurn("watcher", 30*time.Second, func(done turn) bool {
		return done.Trigger == "background"
	})
	if !strings.Contains(woken.ResultText, "<task-notification>") || woken.IsError {
		t.Fatalf("the background turn did not read the command's exit: %+v", woken)
	}
	if woken.StartedAt-first.StartedAt < 4000 {
		t.Fatalf("the command's exit came %dms after the turn that started it, before it ran its 5s", woken.StartedAt-first.StartedAt)
	}
	if woken.CostUSD <= 0 {
		t.Fatalf("the background turn was not priced: %+v", woken)
	}
	hub.waitState("watcher", "asleep", 30*time.Second)
}

// TestTheHubEndsHeldCommandsAndSaysSo: a command still running when the
// hold runs out, or when the loop is paused, ends with the loop's process,
// and the loop's next wake opens with a note saying so and why (#1709).
func TestTheHubEndsHeldCommandsAndSaysSo(t *testing.T) {
	t.Parallel()
	workspace := workspaceWithScript(t, "!bg 600 started a long watch\n!echo\n")
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--background-hold-sec", "3")
	hub.createLoop("holder", map[string]any{
		"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
	})
	first := hub.waitTurn("holder", firstTurnWait, func(turn) bool { return true })
	// held past its idle timeout, then closed at the hold's end
	hub.waitState("holder", "idle", 10*time.Second)
	hub.waitState("holder", "asleep", 30*time.Second)

	hub.message("holder", "anything finish?")
	told := hub.waitTurn("holder", 30*time.Second, func(done turn) bool {
		return done.Trigger == "message" && done.StartedAt > first.StartedAt
	})
	for _, want := range []string{"the hub ended a background command you left running", "3s passed since your last wake that was not a command's exit", "anything finish?"} {
		if !strings.Contains(told.ResultText, want) {
			t.Fatalf("the wake after the hold missed %q:\n%s", want, told.ResultText)
		}
	}
	hub.waitState("holder", "asleep", 30*time.Second)

	// told once: the next wake carries no note
	setScript(t, workspace, "!echo\n")
	hub.message("holder", "and now?")
	later := hub.waitTurn("holder", 30*time.Second, func(done turn) bool {
		return done.StartedAt > told.StartedAt
	})
	if strings.Contains(later.ResultText, "the hub ended") {
		t.Fatalf("the ended commands were told twice:\n%s", later.ResultText)
	}
	hub.waitState("holder", "asleep", 30*time.Second)

	// a pause ends them at once, not at the hold's end
	setScript(t, workspace, "!bg 600 started another\n")
	hub.message("holder", "start another")
	started := hub.waitTurn("holder", 30*time.Second, func(done turn) bool {
		return done.StartedAt > later.StartedAt
	})
	// ended by the pause, not by the hold: the note says which
	hub.mustJSON("POST", "/api/loops/holder/pause", nil, nil)
	hub.waitState("holder", "paused", 10*time.Second)
	time.Sleep(time.Second)
	hub.mustJSON("POST", "/api/loops/holder/resume", nil, nil)
	setScript(t, workspace, "!echo\n")
	hub.message("holder", "back")
	resumed := hub.waitTurn("holder", 30*time.Second, func(done turn) bool {
		return done.StartedAt > started.StartedAt
	})
	if !strings.Contains(resumed.ResultText, "you were paused, killed or switched off") {
		t.Fatalf("the wake after the pause did not say why its command ended:\n%s", resumed.ResultText)
	}
}

// TestAChainOfCommandsEndsWithTheHold: a background turn that starts
// another command does not restart the hold, which counts from the end of
// the last turn the hub started. A chain of commands, each started by the
// turn the last one's exit woke, ends with the hold instead of waking the
// loop for as long as it likes (#1709).
func TestAChainOfCommandsEndsWithTheHold(t *testing.T) {
	t.Parallel()
	// every turn after the first is a command's exit, and starts another
	workspace := workspaceWithScript(t, "!bg 2 started a watch\n!bg 2 started the next\n")
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--background-hold-sec", "7")
	hub.createLoop("chainer", map[string]any{
		"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
	})
	first := hub.waitTurn("chainer", firstTurnWait, func(turn) bool { return true })
	hub.waitTurn("chainer", 10*time.Second, func(done turn) bool { return done.Trigger == "background" })
	// a hold restarted by each background turn would never close it
	hub.waitState("chainer", "asleep", 20*time.Second)

	var chain []turn
	for _, done := range hub.turns("chainer") {
		if done.Trigger == "background" {
			chain = append(chain, done)
		}
	}
	if len(chain) == 0 || len(chain) > 4 {
		t.Fatalf("want a chain of one to four background turns within the 7s hold, got %d: %+v", len(chain), chain)
	}

	setScript(t, workspace, "!echo\n")
	hub.message("chainer", "where did it stop?")
	told := hub.waitTurn("chainer", 30*time.Second, func(done turn) bool {
		return done.Trigger == "message" && done.StartedAt > first.StartedAt
	})
	if !strings.Contains(told.ResultText, "7s passed since your last wake that was not a command's exit") {
		t.Fatalf("the wake after the chain did not say the hold ended it:\n%s", told.ResultText)
	}
}

// TestAHubRestartEndsHeldCommandsAndSaysSo: the hub's processes stop with
// it, so a command running then is gone when it starts again, and the
// loop's first wake on the new hub says the restart ended it (#1709).
func TestAHubRestartEndsHeldCommandsAndSaysSo(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	workspace := workspaceWithScript(t, "!bg 600 started a watch\n!echo\n")
	hub := startServer(t, dataDir)
	hub.createLoop("survivor", map[string]any{
		"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
	})
	hub.waitTurn("survivor", firstTurnWait, func(turn) bool { return true })
	time.Sleep(2 * time.Second) // past the idle timeout: held, not closed
	hub.waitState("survivor", "idle", 10*time.Second)
	hub.stop()

	hub = startServer(t, dataDir)
	hub.message("survivor", "still there?")
	told := hub.waitTurn("survivor", 30*time.Second, func(done turn) bool {
		return done.Trigger == "message"
	})
	if !strings.Contains(told.ResultText, "because the hub restarted") {
		t.Fatalf("the first wake after the restart did not say the restart ended the command:\n%s", told.ResultText)
	}
}

// TestAMessageMeetingACommandsExit: the hub's message can reach the CLI
// as a command exits. The CLI may run the command's turn first and the
// message's after, or both in one turn; either way the message's result
// closes the message's turn, and a turn of the CLI's own is recorded as
// a background turn (#1709).
func TestAMessageMeetingACommandsExit(t *testing.T) {
	t.Parallel()
	for _, order := range []string{"separate", "coalesce"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			workspace := workspaceWithScript(t, "!bg 600 started a watch\n!echo\n")
			hub := startServer(t, dataDir)
			if err := os.MkdirAll(filepath.Join(dataDir, "fkstate"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "fkstate", "bg-race"), []byte(order), 0o644); err != nil {
				t.Fatal(err)
			}
			hub.createLoop("racer", map[string]any{
				"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
			})
			first := hub.waitTurn("racer", firstTurnWait, func(turn) bool { return true })
			time.Sleep(2 * time.Second) // held past the idle timeout
			hub.message("racer", "the message that meets it")

			answered := hub.waitTurn("racer", 30*time.Second, func(done turn) bool {
				return done.Trigger == "message" && done.StartedAt > first.StartedAt
			})
			if !strings.Contains(answered.ResultText, "the message that meets it") {
				t.Fatalf("the message's turn closed on another turn's result:\n%s", answered.ResultText)
			}
			hub.waitState("racer", "asleep", 30*time.Second)
			var background []turn
			for _, done := range hub.turns("racer") {
				if done.Trigger == "background" {
					background = append(background, done)
				}
			}
			switch order {
			case "separate":
				if len(background) != 1 || !strings.Contains(background[0].ResultText, "<task-notification>") ||
					strings.Contains(background[0].ResultText, "the message that meets it") {
					t.Fatalf("the command's own turn was not recorded apart: %+v", background)
				}
				if strings.Contains(answered.ResultText, "<task-notification>") {
					t.Fatalf("the message's turn holds the command's result:\n%s", answered.ResultText)
				}
			case "coalesce":
				if len(background) != 0 {
					t.Fatalf("one turn for both was recorded twice: %+v", background)
				}
				if !strings.Contains(answered.ResultText, "<task-notification>") {
					t.Fatalf("the turn that took both did not read the command's exit:\n%s", answered.ResultText)
				}
			}
		})
	}
}

// TestACommandExitingDuringTheHubsTurn: the CLI reports a command's exit
// as it happens, during the hub's turn (observed on 2.1.326). The turn in
// flight may read it itself and answer it in the same result, as 2.1.326
// was seen doing ("inject"), or the command's own turn may run after that
// turn's result and ahead of the hub's next message ("during"). Either
// way the next message's turn closes on its own result, and only a turn
// of the CLI's own is recorded as a background turn (#1709).
func TestACommandExitingDuringTheHubsTurn(t *testing.T) {
	t.Parallel()
	for _, order := range []string{"inject", "during"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			workspace := workspaceWithScript(t, "!bg 600 started a watch\n!hang 3\n!echo\n")
			hub := startServer(t, dataDir)
			if err := os.MkdirAll(filepath.Join(dataDir, "fkstate"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dataDir, "fkstate", "bg-race"), []byte(order), 0o644); err != nil {
				t.Fatal(err)
			}
			hub.createLoop("racer", map[string]any{
				"workspace_path": workspace, "workspace_mode": "dir", "idle_timeout_sec": 1,
			})
			first := hub.waitTurn("racer", firstTurnWait, func(turn) bool { return true })
			time.Sleep(2 * time.Second) // held past the idle timeout
			hub.message("racer", "the message the command exits during")
			hub.waitState("racer", "busy", 10*time.Second)
			hub.message("racer", "the message queued behind it")

			queued := hub.waitTurn("racer", 30*time.Second, func(done turn) bool {
				return done.Trigger == "message" && strings.Contains(done.ResultText, "the message queued behind it")
			})
			hub.waitState("racer", "asleep", 30*time.Second)
			if strings.Contains(queued.ResultText, "<task-notification>") {
				t.Fatalf("the queued message's turn closed on the command's result:\n%s", queued.ResultText)
			}
			var background, messages []turn
			for _, done := range hub.turns("racer") {
				if done.StartedAt <= first.StartedAt {
					continue
				}
				switch done.Trigger {
				case "background":
					background = append(background, done)
				case "message":
					messages = append(messages, done)
				}
			}
			if len(messages) != 2 {
				t.Fatalf("want the two message turns, got %+v", messages)
			}
			switch order {
			case "inject":
				if len(background) != 0 {
					t.Fatalf("a turn the hub started was recorded as a background turn: %+v", background)
				}
				// turns come in no set order: the queued message's is the other one
				during := messages[0]
				if during.ID == queued.ID {
					during = messages[1]
				}
				if !strings.Contains(during.ResultText, "hung 3s") || !strings.Contains(during.ResultText, "<task-notification>") {
					t.Fatalf("the turn the command exited during did not answer it:\n%s", during.ResultText)
				}
			case "during":
				if len(background) != 1 || !strings.Contains(background[0].ResultText, "<task-notification>") {
					t.Fatalf("the command's own turn was not recorded as a background turn: %+v", background)
				}
				for _, done := range messages {
					if strings.Contains(done.ResultText, "<task-notification>") {
						t.Fatalf("a message's turn holds the command's result: %+v", done)
					}
				}
			}
		})
	}
}

// TestACapEndsHeldCommands: a command's exit starts a turn the hub cannot
// stop, so a loop the daily cap holds keeps no command running; raising
// the cap wakes it, told what the cap ended (#1709). fakeclaude's turns
// cost 0.001 each, so a cap of 0.0005 holds after the first.
func TestACapEndsHeldCommands(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.createLoop("thrifty", map[string]any{
		"workspace_path": workspaceWithScript(t, "!bg 600 started a watch\n!echo\n"),
		"workspace_mode": "dir", "idle_timeout_sec": 600, "daily_cap_usd": 0.0005,
	})
	first := hub.waitTurn("thrifty", firstTurnWait, func(turn) bool { return true })
	// capped at the turn's end, and closed there rather than held for the
	// command or kept for the 600s idle timeout: had it lived on, the wake
	// below would find its command running and be told nothing
	hub.waitState("thrifty", "capped", 15*time.Second)

	hub.mustJSON("PATCH", "/api/loops/thrifty", map[string]any{"daily_cap_usd": 1}, nil)
	told := hub.waitTurn("thrifty", 30*time.Second, func(done turn) bool {
		return done.StartedAt > first.StartedAt
	})
	if !strings.Contains(told.ResultText, "because a cap took hold") {
		t.Fatalf("the wake after the cap lifted did not say the cap ended the command:\n%s", told.ResultText)
	}
}
