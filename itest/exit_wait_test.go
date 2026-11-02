//go:build integration

package itest

import (
	"testing"
	"time"
)

// A claude that ends its turn, closes its output and lives on no longer
// holds its loop for as long as it lives: the hub gives it the exit grace,
// ends it, and a message sent meanwhile gets its turn long before the
// process would have exited by itself (#1247).
func TestAClaudeThatClosesItsOutputAndLivesOnIsEnded(t *testing.T) {
	t.Parallel()
	workspace := workspaceWithScript(t, "ok\n!wedge 300\nback to work\n")
	hub := startServer(t, t.TempDir())
	hub.createLoop("alpha", map[string]any{"workspace_path": workspace})
	hub.waitCompleted("alpha", 1)

	hub.message("alpha", "the last answer before the wedge")
	hub.waitCompleted("alpha", 2)
	wedgedAt := time.Now()
	hub.message("alpha", "are you still there?")
	after := hub.waitTurn("alpha", 120*time.Second, func(answer turn) bool { return answer.ResultText == "back to work" })
	if waited := time.Since(wedgedAt); waited > 100*time.Second {
		t.Fatalf("the message waited %s for its turn, want the exit grace and a kill, not the wedge", waited)
	}
	if after.IsError || after.Trigger != "message" {
		t.Fatalf("the turn after the wedge = %s, want the message answered", dump(after))
	}
}
