//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// The fakeclaude directives the suite relied on but never exercised on their
// own (#3). Each pins engine behavior the directive exists to provoke, not
// the fake: a process that dies mid-turn, a reply far past one read buffer,
// and a turn still running when the operator reaches for it.

// TestCrashedTurnIsRecordedAndTheLoopIsNotWedged: a process that exits
// mid-turn leaves the turn errored, saying why (#1096), rather than open
// forever, and the loop still takes the next message — it starts a turn
// for it and finishes that one too. Every turn crashes here, by design:
// what is pinned is that a crash costs a turn rather than the loop, not
// that the reply after one is any good.
func TestCrashedTurnIsRecordedAndTheLoopIsNotWedged(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("brittle", map[string]any{
		"workspace_path": workspaceWithScript(t, "!crash\n"),
		"workspace_mode": "dir",
	})

	s.message("brittle", "first")
	failed := s.waitTurn("brittle", 30*time.Second, func(tr turn) bool {
		return tr.IsError
	})
	if failed.EndedAt == 0 {
		t.Fatal("the crashed turn was left open")
	}
	if !strings.Contains(failed.ResultText, "claude exited (code 2) before the turn ended") {
		t.Errorf("the crashed turn says %q, want why it ended (#1096)", failed.ResultText)
	}

	s.message("brittle", "second")
	next := s.waitTurn("brittle", 30*time.Second, func(tr turn) bool {
		return tr.StartedAt > failed.StartedAt && tr.Trigger == "message"
	})
	if next.EndedAt == 0 {
		t.Fatal("the turn after a crash was left open")
	}
}

// TestHugeReplyCrossesTheStreamReader: claude's stream-json is one JSON
// object per line, and a reply can be far larger than any single read. The
// runner reads 256KB at a time (internal/claude/stream.go), so a 512KB reply
// spans buffers and must still arrive whole — a truncated result would be a
// silently corrupted turn rather than a visible failure.
func TestHugeReplyCrossesTheStreamReader(t *testing.T) {
	t.Parallel()
	const size = 512 * 1024
	s := startServer(t, t.TempDir())
	s.createLoop("verbose", map[string]any{
		"workspace_path": workspaceWithScript(t, "!huge 524288\n"),
		"workspace_mode": "dir",
	})

	s.message("verbose", "say a lot")
	big := s.waitTurn("verbose", 60*time.Second, func(tr turn) bool {
		return len(tr.ResultText) > 1024
	})
	if len(big.ResultText) != size {
		t.Fatalf("reply came back %d bytes, want the scripted %d", len(big.ResultText), size)
	}
	if strings.Trim(big.ResultText, "x") != "" {
		t.Fatal("the reply is not the scripted payload; something spliced the buffers")
	}
	if big.IsError {
		t.Fatal("a large but well-formed reply was recorded as an error")
	}
}

// TestKillEndsATurnInFlight: an operator who kills a working loop must get
// the turn closed as errored rather than left hanging, and the loop must
// settle asleep instead of waiting on a process that is gone.
func TestKillEndsATurnInFlight(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("stuck", map[string]any{
		"workspace_path": workspaceWithScript(t, "!hang 30\n"),
		"workspace_mode": "dir",
	})

	// whichever turn gets there first — the creation tick or the message —
	// hangs, and the kill has to land on the one actually running
	s.message("stuck", "take your time")
	running := s.waitRunningTurn("stuck", 30*time.Second, func(turn) bool { return true })
	s.mustJSON("POST", "/api/loops/stuck/kill", nil, nil)

	killed := s.waitTurn("stuck", 30*time.Second, func(tr turn) bool {
		return tr.ID == running.ID
	})
	if !killed.IsError {
		t.Fatalf("the killed turn finished clean: %+v", killed)
	}
	if killed.ResultText != "killed by the operator" {
		t.Errorf("the killed turn says %q, want the hub's reason, not the process's exit (#1096)", killed.ResultText)
	}
	s.waitState("stuck", "asleep", 30*time.Second)
}

// A session lost mid-turn errors the turn, saying so (#1096), and the fresh
// session's preamble carries the loop's replies but not that reason: it is
// the hub's word, not the loop's.
func TestALostSessionsTurnSaysWhyAndStaysOutOfTheReplies(t *testing.T) {
	t.Parallel()
	// line 1 absorbs the creation tick; line 2 dies the way a lost session
	// does; the fresh session's turn 1 is line 1 again and echoes its batch,
	// preamble and all.
	hub := startServer(t, t.TempDir())
	hub.createLoop("fennel", map[string]any{"workspace_path": workspaceWithScript(t, "!ctx 0\n!lost\n")})
	hub.waitTurn("fennel", firstTurnWait, func(tn turn) bool { return tn.Trigger == "tick" })

	hub.message("fennel", "carry on")
	lost := hub.waitTurn("fennel", 30*time.Second, func(tn turn) bool { return tn.IsError })
	if lost.ResultText != "the session could not be resumed; the loop goes on in a fresh one" {
		t.Errorf("the lost session's turn says %q, want why it ended", lost.ResultText)
	}
	fresh := hub.waitTurn("fennel", 30*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "carry on")
	})
	if !strings.Contains(fresh.ResultText, "could not be resumed; this is a fresh session") {
		t.Fatalf("the fresh session had no preamble: %s", dump(fresh))
	}
	if strings.Contains(fresh.ResultText, "the loop goes on in a fresh one") {
		t.Errorf("the lost turn's reason reached the fresh session as a reply: %s", dump(fresh))
	}
}
