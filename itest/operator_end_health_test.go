//go:build integration

package itest

import (
	"testing"
	"time"
)

// A turn the operator ended is no evidence of a failing loop, so a run of
// kills opens no turn_errors problem, while a run of turns whose process
// crashes on its own still does (ADR-0053, #1420). The threshold is set to
// two to keep the crash ladder short; a killed or crashed turn uses up no
// script line, so each message replays the same one.
func TestAnOperatorEndedTurnIsNoErroredTurn(t *testing.T) {
	t.Parallel()
	const threshold = 2

	killed := startServer(t, t.TempDir())
	killed.mustJSON("PUT", "/api/settings", map[string]any{"health_error_turns": threshold}, nil)
	killed.createLoop("held", map[string]any{"workspace_path": workspaceWithScript(t, "hello\n!hang 600\n")})
	killed.waitCompleted("held", 1)
	killed.waitState("held", "asleep", 30*time.Second)
	for kills := 1; kills <= threshold+1; kills++ {
		killed.message("held", "a long question")
		killed.waitState("held", "busy", 30*time.Second)
		killed.mustJSON("POST", "/api/loops/held/kill", nil, nil)
		killed.waitState("held", "asleep", 30*time.Second)
		killed.waitCompleted("held", 1+kills)
	}
	time.Sleep(time.Second)
	if view := killed.loop("held"); view.Health != "ok" || len(view.Problems) != 0 {
		t.Fatalf("after %d kills: health %q, problems %+v; want ok and none", threshold+1, view.Health, view.Problems)
	}

	crashed := startServer(t, t.TempDir())
	crashed.mustJSON("PUT", "/api/settings", map[string]any{"health_error_turns": threshold}, nil)
	crashed.createLoop("crashed", map[string]any{"workspace_path": workspaceWithScript(t, "hello\n!crash\n")})
	crashed.waitCompleted("crashed", 1)
	crashed.waitState("crashed", "asleep", 30*time.Second)
	crashed.message("crashed", "this one dies")
	// the first crash's retry, ten seconds on, is the second errored turn
	var view loopView
	within(t, 60*time.Second, "crashed's turn_errors problem", func() bool {
		view = crashed.loop("crashed")
		return view.Health == "warning"
	})
	if len(view.Problems) != 1 || view.Problems[0].Symptom != "turn_errors" {
		t.Fatalf("problems = %+v, want one turn_errors", view.Problems)
	}
}
