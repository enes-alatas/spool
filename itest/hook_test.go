//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The hub pins its PreToolUse hook on every loop's claude (ADR-0042): a
// Bash call that breaks a shared-state rule is refused before it runs, and
// the loop reads why. The workspace below asks for every hook to be turned
// off, which is how a loop or a hostile issue would unload the guard; the
// hub's --settings outranks it (#529).
func TestTheHookRefusesASharedStateCommand(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	refused := workspaceWithScript(t, "!bash git stash pop\n")
	if err := os.MkdirAll(filepath.Join(refused, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"settings.json", "settings.local.json"} {
		if err := os.WriteFile(filepath.Join(refused, ".claude", file), []byte(`{"disableAllHooks": true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.createLoop("stasher", map[string]any{"workspace_path": refused, "workspace_mode": "dir"})
	s.createLoop("lister", map[string]any{"workspace_path": workspaceWithScript(t, "!bash git stash list\n"), "workspace_mode": "dir"})

	s.message("stasher", "put your work back")
	blocked := s.waitTurn("stasher", 30*time.Second, func(tr turn) bool { return tr.Trigger == "message" })
	if !strings.HasPrefix(blocked.ResultText, "blocked: Refused by Spool: ") || !strings.Contains(blocked.ResultText, "stash") {
		t.Fatalf("git stash pop was not refused with a reason: %q", blocked.ResultText)
	}

	s.message("lister", "what is stashed")
	ran := s.waitTurn("lister", 30*time.Second, func(tr turn) bool { return tr.Trigger == "message" })
	if ran.ResultText != "ran: git stash list" {
		t.Fatalf("an allowed call did not run: %q", ran.ResultText)
	}
}

// dockerHookRefusesASharedStateCommand: a contained loop runs the hook its
// image carries, through the same pinned --settings.
func dockerHookRefusesASharedStateCommand(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsstash", nil)
	cleanupWorkstation(t, s.loop("wsstash").ID)
	s.scriptLoop("wsstash", "!bash pkill -f 'go test'\n")

	s.message("wsstash", "stop the tests")
	blocked := s.waitTurn("wsstash", 90*time.Second, func(tr turn) bool {
		return tr.Trigger == "message" && strings.HasPrefix(tr.ResultText, "blocked: ")
	})
	if !strings.Contains(blocked.ResultText, "Refused by Spool: pkill") {
		t.Fatalf("pkill -f was not refused with a reason: %q", blocked.ResultText)
	}
}
