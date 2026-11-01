//go:build integration

package itest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitCompleted blocks until the loop has finished n turns, errored or not.
func (s *server) waitCompleted(name string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.completed(name)) >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("%s finished %d turns, want %d", name, len(s.completed(name)), n)
}

// waitHealth polls the loop record until its health reads want, and
// returns the record.
func (s *server) waitHealth(name, want string) loopView {
	s.t.Helper()
	var view loopView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if view = s.loop(name); view.Health == want {
			return view
		}
	}
	s.t.Fatalf("%s's health = %q with problems %+v, want %q", name, view.Health, view.Problems, want)
	return view
}

// A loop whose turns keep failing tells nobody by itself, so the hub tells
// its owner (ADR-0053, #764): once on the third errored turn in a row, not
// again while it stays open, once more when a turn succeeds, again when it
// recurs, and once when the operator acknowledges it, after which the
// count starts over.
func TestAFailingLoopIsToldToTheOwnerOnce(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7474, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "ok\nok\n!error\n!error\n!error\n!error\nback to work\n!error\n!error\n!error\n!error\nfine\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})
	// the tick at its creation, then the owner's first DM, each its own turn
	srv.waitCompleted("alpha", 1)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")
	srv.waitCompleted("alpha", 2)

	take := func(n int) {
		t.Helper()
		srv.message("alpha", fmt.Sprintf("turn %d", n))
		srv.waitCompleted("alpha", n)
	}
	const openedText, recoveredText, ackedText = "turns all ended in an error", "without an error again", "cleared in the control room"

	take(3)
	take(4)
	if view := srv.loop("alpha"); view.Health != "ok" || countSent(tg, operator.ID, openedText) != 0 {
		t.Fatalf("after two errored turns: health %q, problems %+v; want ok, untold", view.Health, view.Problems)
	}
	take(5)
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha's last 3 turns all ended in an error")
	view := srv.waitHealth("alpha", "warning")
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "turn_errors" || view.Problems[0].Since == 0 {
		t.Fatalf("problems = %+v, want one turn_errors with a since", view.Problems)
	}
	if !srv.hasEvent("alpha", "owner_notice", 5*time.Second) {
		t.Fatal("the owner was told without an owner_notice on alpha's timeline")
	}
	take(6)
	time.Sleep(time.Second)
	if n := countSent(tg, operator.ID, openedText); n != 1 {
		t.Fatalf("a fourth errored turn: the owner was told %d times, want once", n)
	}

	take(7)
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha finished a turn "+recoveredText)
	srv.waitHealth("alpha", "ok")

	take(8)
	take(9)
	take(10)
	srv.waitHealth("alpha", "warning")
	for deadline := time.Now().Add(10 * time.Second); countSent(tg, operator.ID, openedText) < 2; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the owner was not told when the errors came back")
		}
	}

	srv.mustJSON("POST", "/api/loops/alpha/problems/turn_errors/acknowledge", nil, &view)
	if view.Health != "ok" || len(view.Problems) != 0 {
		t.Fatalf("after acknowledging: health %q, problems %+v; want ok and none", view.Health, view.Problems)
	}
	tg.waitSentFrom(t, operator.ID, "alpha", "errored turns was "+ackedText)
	// the count started over: one more errored turn opens nothing
	take(11)
	time.Sleep(time.Second)
	if view := srv.loop("alpha"); view.Health != "ok" || countSent(tg, operator.ID, openedText) != 2 {
		t.Fatalf("one errored turn after acknowledging: health %q, told %d times; want ok and twice",
			view.Health, countSent(tg, operator.ID, openedText))
	}
	if n := countSent(tg, operator.ID, recoveredText); n != 1 {
		t.Fatalf("the owner was told of a recovery %d times, want once", n)
	}
}

// A workstation that stays down past the grace period is a problem the
// owner hears of once, and once more when it is back up (ADR-0053, #764).
// The problem is stored, so one still open when the hub restarts is
// neither told again nor left open once the workstation is up.
func TestADownWorkstationIsToldToTheOwnerOnce(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7575, First: "Operator", Username: "operator"}
	tg := startFakeTelegram(t, "alpha")
	dataDir := t.TempDir()
	start := func() *server {
		return startServerArgs(t, dataDir, "--runtime", "bare",
			"--telegram-api-base", tg.srv.URL,
			"--telegram-bind-settle-sec", "1",
			"--telegram-answer-timeout-sec", "5",
			"--problem-down-grace-sec", "1")
	}
	srv := start()
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})
	srv.allowOwner(tg, "alpha", operator)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")

	const downText, upText = "alpha's workstation has been down since", "alpha's workstation is up again"
	failed := filepath.Join(srv.fkState, "mcp-failed")
	if err := os.MkdirAll(srv.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	// goDown makes the next spawn unable to reach the hub, ends the live
	// one so that the next message spawns another, and waits for the
	// owner to have been told that many times.
	goDown := func(told int) {
		t.Helper()
		if err := os.WriteFile(failed, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		srv.mustJSON("POST", "/api/loops/alpha/kill", nil, nil)
		// the kill returns before the process is gone; a message reaching
		// it while it dies is a crash, and the next spawn waits the ladder
		srv.waitState("alpha", "asleep", 30*time.Second)
		srv.message("alpha", "wake up")
		srv.waitState("alpha", "workstation_down", 30*time.Second)
		for deadline := time.Now().Add(20 * time.Second); countSent(tg, operator.ID, downText) < told; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the owner was told of a down workstation %d times, want %d", countSent(tg, operator.ID, downText), told)
			}
		}
	}

	goDown(1)
	view := srv.waitHealth("alpha", "failing")
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "workstation_down" {
		t.Fatalf("problems = %+v, want one workstation_down", view.Problems)
	}
	time.Sleep(2 * time.Second)
	if n := countSent(tg, operator.ID, downText); n != 1 {
		t.Fatalf("the owner was told of a down workstation %d times, want once", n)
	}
	if err := os.Remove(failed); err != nil {
		t.Fatal(err)
	}
	srv.message("alpha", "are you back")
	tg.waitSentFrom(t, operator.ID, "alpha", upText)
	srv.waitHealth("alpha", "ok")

	// down again, and the hub restarts with the problem open and the
	// workstation fixed meanwhile
	goDown(2)
	srv.stop()
	if err := os.Remove(failed); err != nil {
		t.Fatal(err)
	}
	srv = start()
	srv.message("alpha", "after the restart")
	srv.waitHealth("alpha", "ok")
	for deadline := time.Now().Add(20 * time.Second); countSent(tg, operator.ID, upText) < 2; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the owner was not told the workstation is up after the restart")
		}
	}
	if n := countSent(tg, operator.ID, downText); n != 2 {
		t.Fatalf("the owner was told of a down workstation %d times across the restart, want twice", n)
	}
}

// A turn that runs past the limit is a problem the owner hears of once,
// dated from the turn's start, and the hub leaves the turn running. The
// owner's Kill ends the turn, which clears the problem, and they hear of
// that once too (#1142).
func TestATurnPastItsLimitIsToldToTheOwner(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7575, First: "Operator", Username: "operator"}
	workspace := workspaceWithScript(t, "ok\nok\n!hang 600\n")
	hub, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspace})
	hub.waitCompleted("alpha", 1)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, hub, "alpha")
	hub.waitCompleted("alpha", 2)
	hub.mustJSON("PUT", "/api/settings", map[string]any{"health_turn_minutes": 1}, nil)

	hub.message("alpha", "the long question")
	hub.waitState("alpha", "busy", 30*time.Second)
	var view loopView
	within(t, 90*time.Second, "alpha's turn_stuck problem", func() bool {
		view = hub.loop("alpha")
		return view.Health == "warning"
	})
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "turn_stuck" || view.Problems[0].Since == 0 {
		t.Fatalf("problems = %+v, want one turn_stuck with a since", view.Problems)
	}
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha's turn has been running since")
	if state := hub.loop("alpha").State; state != "busy" {
		t.Fatalf("alpha is %q past its limit, want the turn left running", state)
	}
	time.Sleep(time.Second)
	if told := countSent(tg, operator.ID, "turn has been running since"); told != 1 {
		t.Fatalf("the owner was told of one long turn %d times, want once", told)
	}

	hub.mustJSON("POST", "/api/loops/alpha/kill", nil, nil)
	hub.waitHealth("alpha", "ok")
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha's long turn has ended")
}

// A loop due to wake that starts no turn, though nothing holds it, is a
// problem the owner hears of once, dated from the wake. Here the loop's
// actor stops answering: its claude ends a turn, closes its output and
// lives on, and the hub's wait for that exit holds the actor, so a tick
// asked of it meanwhile goes unread. The turn the tick starts once the
// process exits clears the problem, and the owner hears of that once too.
// A paused loop's tick is no wake it owes, so it opens nothing (#1143).
func TestAWakeThatStartsNoTurnIsToldToTheOwner(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7676, First: "Operator", Username: "operator"}
	workspace := workspaceWithScript(t, "ok\nok\n!wedge 150\nback to work\n")
	hub, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspace})
	hub.waitCompleted("alpha", 1)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, hub, "alpha")
	hub.waitCompleted("alpha", 2)
	hub.mustJSON("PUT", "/api/settings", map[string]any{"health_wake_grace_minutes": 1}, nil)

	hub.mustJSON("POST", "/api/loops/beta/pause", nil, nil)
	hub.waitState("beta", "paused", 30*time.Second)
	hub.mustJSON("POST", "/api/loops/beta/wake", nil, nil)

	hub.message("alpha", "the last answer before the wedge")
	hub.waitCompleted("alpha", 3)
	hub.mustJSON("POST", "/api/loops/alpha/wake", nil, nil)
	var view loopView
	within(t, 120*time.Second, "alpha's wake_missed problem", func() bool {
		view = hub.loop("alpha")
		return view.Health == "failing"
	})
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "wake_missed" || view.Problems[0].Since == 0 {
		t.Fatalf("problems = %+v, want one wake_missed with a since", view.Problems)
	}
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha has been due to wake since")
	if beta := hub.loop("beta"); beta.Health != "ok" || len(beta.Problems) != 0 {
		t.Fatalf("paused beta's health = %q with problems %+v after a tick, want ok", beta.Health, beta.Problems)
	}
	time.Sleep(time.Second)
	if told := countSent(tg, operator.ID, "has been due to wake since"); told != 1 {
		t.Fatalf("the owner was told of one missed wake %d times, want once", told)
	}

	within(t, 150*time.Second, "a turn clearing alpha's wake_missed problem", func() bool {
		return hub.loop("alpha").Health == "ok"
	})
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha has started a turn again")
	hub.waitCompleted("alpha", 4)
}
