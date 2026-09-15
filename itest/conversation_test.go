//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// TestSeparateTurnsPerConversation: a control-room message and a group
// message queued behind a busy turn must not blend into one answer — the
// actor runs one turn per conversation (ADR-0026).
func TestSeparateTurnsPerConversation(t *testing.T) {
	// alpha's first turn hangs long enough for both arrivals to queue;
	// afterwards it echoes (a bare !ctx keeps the echo reply), so each
	// turn's reply names its own inputs.
	wsAlpha := workspaceWithScript(t, "!hang 4\n!ctx 0\n")
	wsBeta := workspaceWithScript(t, `!send {"destination":"group","text":"@alpha group ping"}`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("alpha", map[string]any{"workspace_path": wsAlpha})
	s.createLoop("beta", map[string]any{"workspace_path": wsBeta})
	writeFakeMCPConfig(t, s, "beta")

	s.message("alpha", "start hanging")
	time.Sleep(300 * time.Millisecond) // alpha is inside the hang
	s.message("alpha", "web note")     // control_room conversation
	s.message("beta", "go")            // beta's turn group-sends to alpha

	webTurn := s.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "web note")
	})
	groupTurn := s.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "group ping")
	})
	if webTurn.ID == groupTurn.ID {
		t.Fatalf("control_room and group inputs shared one turn: %s", dump(webTurn))
	}
	if strings.Contains(webTurn.ResultText, "group ping") || strings.Contains(groupTurn.ResultText, "web note") {
		t.Fatalf("conversation inputs blended across turns:\nweb: %s\ngroup: %s",
			webTurn.ResultText, groupTurn.ResultText)
	}
}
