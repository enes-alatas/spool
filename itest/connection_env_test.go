//go:build integration

package itest

import (
	"testing"
	"time"
)

// An env-var connection attached to or detached from an awake loop reaches
// it from its next turn (#640). The process keeps the env it was spawned
// with, so an idle one closes at once and one mid-turn closes when its turn
// ends. Work queued meanwhile waits for the new process. The session
// resumes: no handoff turn, no rotation. The marker value is shorter than
// the redactor acts on, so every turn can show the value it ran with.
func TestAnEnvChangeReachesTheNextTurn(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	// turn 1 is the creation tick's; the idle timeout keeps the process
	// alive between turns for as long as the test runs
	workspace := workspaceWithScript(t, "!echo\n!env MARKER\n!env MARKER\n!hang 3\n!env MARKER\n")
	s.createLoop("aster", map[string]any{"workspace_path": workspace, "idle_timeout_sec": 600})
	s.waitTurn("aster", 30*time.Second, func(turn) bool { return true })
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "marker", "kind": "env-var", "config": map[string]any{"env": "MARKER"}, "secret": "one",
	}, nil)

	s.message("aster", "before")
	before := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=" })
	s.waitState("aster", "idle", 30*time.Second)

	// idle: the process closes now, and the next turn has the variable
	s.mustJSON("PUT", "/api/loops/aster/connections/marker", nil, nil)
	s.waitState("aster", "asleep", 30*time.Second)
	s.message("aster", "attached")
	attached := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=one" })
	s.waitState("aster", "idle", 30*time.Second)

	// mid-turn: the turn finishes on the old env, and the message queued
	// behind it runs in a process spawned without the variable
	s.message("aster", "hang")
	s.waitState("aster", "busy", 30*time.Second)
	s.mustJSON("DELETE", "/api/loops/aster/connections/marker", nil, nil)
	s.message("aster", "detached")
	detached := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=" && tn.ID != before.ID })

	for _, tn := range []turn{attached, detached} {
		if tn.SessionID != before.SessionID {
			t.Fatalf("turn %s ran on session %s, want the resumed %s", tn.ResultText, tn.SessionID, before.SessionID)
		}
	}
	for _, tn := range s.turns("aster") {
		if tn.Trigger == "rotation" {
			t.Fatalf("an env change took a handoff turn: %s", dump(tn))
		}
	}
	for _, e := range s.eventsQuery("aster", "limit=500") {
		if e.Type == "spool" && e.Subtype == "context_rotated" {
			t.Fatalf("an env change rotated the context: %s", e.Payload)
		}
	}
}

// A rotation due at the boundary where an env change closes the process
// still runs there: its handoff spawns a fresh process anyway, and the wake
// after it reads the new env. Closing instead parked the handoff until the
// next wake, which could be a tick hours away.
func TestAnEnvChangeDoesNotDeferADueRotation(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	// fakeclaude counts lines per session: the fresh session's first turn
	// is line 1 again
	workspace := workspaceWithScript(t, "!env MARKER\n!hang 3\nhandoff note\n")
	s.createLoop("aster", map[string]any{"workspace_path": workspace, "idle_timeout_sec": 600})
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "marker", "kind": "env-var", "config": map[string]any{"env": "MARKER"}, "secret": "one",
	}, nil)

	// both land mid-turn, and nothing is queued behind the turn
	s.message("aster", "hang")
	s.waitState("aster", "busy", 30*time.Second)
	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)
	s.mustJSON("PUT", "/api/loops/aster/connections/marker", nil, nil)

	hang := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "hung 3s" })
	handoff := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return tn.Trigger == "rotation" && tn.ResultText == "handoff note"
	})
	if handoff.StartedAt < hang.EndedAt {
		t.Fatalf("handoff started at %d, before the running turn ended at %d", handoff.StartedAt, hang.EndedAt)
	}

	s.message("aster", "after")
	after := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=one" })
	if after.SessionID == handoff.SessionID {
		t.Fatalf("work after the rotation stayed on the retired session %s", handoff.SessionID)
	}
}
