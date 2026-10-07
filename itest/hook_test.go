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

// The hook refuses a gh body that @-mentions a fleet loop, with the names
// the hub hands it at each wake (#628), and the operator's switch turns the
// refusal off for the next wake.
func TestTheHookRefusesAMentionOfAFleetName(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	const comment = `!bash gh pr comment 12 --body "ready for review, @bravo"` + "\n"
	s.createLoop("alpha", map[string]any{"workspace_path": workspaceWithScript(t, comment), "workspace_mode": "dir"})
	s.createLoop("bravo", nil)
	var settings struct {
		MentionGuard bool `json:"mention_guard"`
	}
	s.mustJSON("GET", "/api/settings", nil, &settings)
	if !settings.MentionGuard {
		t.Fatal("the mention guard is off by default, want on")
	}

	s.message("alpha", "tell bravo")
	blocked := s.waitTurn("alpha", 30*time.Second, func(tr turn) bool { return tr.Trigger == "message" })
	if !strings.HasPrefix(blocked.ResultText, "blocked: Refused by Spool: ") || !strings.Contains(blocked.ResultText, "@-mentions bravo") {
		t.Fatalf("a gh body mentioning a fleet loop was not refused with a reason: %q", blocked.ResultText)
	}

	s.mustJSON("PUT", "/api/settings", map[string]any{"mention_guard": false}, &settings)
	if settings.MentionGuard {
		t.Fatal("PUT mention_guard false left it on")
	}
	s.waitState("alpha", "asleep", 30*time.Second)
	s.message("alpha", "tell bravo again")
	ran := s.waitTurn("alpha", 30*time.Second, func(tr turn) bool { return tr.Trigger == "message" && tr.ID != blocked.ID })
	if !strings.HasPrefix(ran.ResultText, "ran: gh pr comment") {
		t.Fatalf("with the guard off, the call did not run: %q", ran.ResultText)
	}
}

// dockerHookRefusesASharedStateCommand: a contained loop runs the hook its
// image carries, through the same pinned --settings, with the names the hub
// hands it (#628).
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

	s.scriptLoop("wsstash", `!bash gh issue comment 12 --body "taking this, @wsstash"`+"\n")
	s.message("wsstash", "claim it")
	mention := s.waitTurn("wsstash", 90*time.Second, func(tr turn) bool {
		return tr.Trigger == "message" && tr.ID != blocked.ID && strings.HasPrefix(tr.ResultText, "blocked: ")
	})
	if !strings.Contains(mention.ResultText, "@-mentions wsstash") {
		t.Fatalf("a gh body mentioning a fleet loop was not refused: %q", mention.ResultText)
	}
}
