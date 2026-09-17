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
// mid-turn leaves the turn errored rather than open forever, and the loop
// still takes the next message — it starts a turn for it and finishes that
// one too. Every turn crashes here, by design: what is pinned is that a
// crash costs a turn rather than the loop, not that the reply after one is
// any good.
func TestCrashedTurnIsRecordedAndTheLoopIsNotWedged(t *testing.T) {
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
	s.waitState("stuck", "asleep", 30*time.Second)
}
