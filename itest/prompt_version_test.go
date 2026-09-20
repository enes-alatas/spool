//go:build integration

package itest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPromptNamesTheBuildThatWokeTheLoop: the version a loop is told is the
// version the binary reports, resolved in the running process rather than
// written down anywhere (#225). The suite asks the server what it is and then
// asks a loop what it was told; they have to match, or a loop citing its
// build in an argument is citing a guess.
func TestPromptNamesTheBuildThatWokeTheLoop(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})

	var served struct {
		Version string `json:"version"`
	}
	s.mustJSON("GET", "/api/version", nil, &served)
	if served.Version == "" {
		t.Fatal("the server reports no version, so there is nothing for a loop to be told")
	}

	at := time.Now().UnixMilli()
	s.message("aster", "which spool is this")
	prompt := waitPrompt(t, s, "aster", at)

	want := "You run on Spool " + served.Version + "."
	if !strings.Contains(prompt.ResultText, want) {
		t.Fatalf("the loop was not told which Spool woke it (want %q):\n%s", want, prompt.ResultText)
	}

	// and it is the same string the operator sees at a terminal
	out, err := exec.Command(filepath.Join(repoRoot(t), "bin", "spool"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("spool --version: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), served.Version) {
		t.Errorf("--version prints %q, which is not the version the loop was told (%q)", strings.TrimSpace(string(out)), served.Version)
	}
}
