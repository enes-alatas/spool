//go:build integration

package itest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ported from scripts/e2e/m6.sh (#3). The scenario's second half — a loop
// asked to commit a file, and the commit landing on its own branch — needs a
// model that can run git, so it stays tier 3. What tier 2 can own is the
// engine's side of the promise: that two loops sharing a repo are given
// separate worktrees on their own branches, that each turn runs inside its
// own, and that deleting a loop prunes the worktree without taking the branch
// (and never touches main).

func gitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=itest", "GIT_AUTHOR_EMAIL=itest@spool",
			"GIT_COMMITTER_NAME=itest", "GIT_COMMITTER_EMAIL=itest@spool")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return repo
}

func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestLoopsSharingARepoGetTheirOwnWorktrees(t *testing.T) {
	repo := gitRepo(t)
	mainSHA := gitOut(t, repo, "rev-parse", "main")
	dataDir := t.TempDir()
	s := startServer(t, dataDir)

	for _, name := range []string{"alpha", "beta"} {
		s.createLoop(name, map[string]any{"workspace_path": repo})
		view := s.loop(name)
		want := filepath.Join(dataDir, "worktrees", name)
		if view.WorkspacePath != want {
			t.Fatalf("%s works in %q, want its own worktree at %q", name, view.WorkspacePath, want)
		}
		if worktrees := gitOut(t, repo, "worktree", "list"); !strings.Contains(worktrees, "loop/"+name) {
			t.Fatalf("no worktree on branch loop/%s:\n%s", name, worktrees)
		}
	}

	// Each loop's turns must run inside its own worktree, not the shared
	// repo: an untracked script in one is invisible to the other, so the
	// replies say which directory each process was actually given.
	for _, name := range []string{"alpha", "beta"} {
		script := filepath.Join(dataDir, "worktrees", name, ".fakeclaude")
		if err := os.WriteFile(script, []byte("working in "+name+"'s worktree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		// A process reads its script when it spawns, so the creation tick's
		// process has to be gone before the script means anything (#117).
		// Waiting on asleep alone is enough here, where scriptLoop also
		// waits for a turn: the script is already on disk before this wait
		// begins, so even a spawn that starts while the state still reads
		// asleep reads the right file. scriptLoop writes a secret instead,
		// which a wake already assembling its environment has gone past.
		s.waitState(name, "asleep", 30*time.Second)
	}
	for _, name := range []string{"alpha", "beta"} {
		s.message(name, "where are you")
		answered := s.waitTurn(name, 30*time.Second, func(tr turn) bool {
			return strings.Contains(tr.ResultText, "worktree")
		})
		if !strings.Contains(answered.ResultText, "working in "+name+"'s worktree") {
			t.Fatalf("%s answered from the wrong directory: %q", name, answered.ResultText)
		}
	}

	// deleting a loop prunes its worktree and keeps its branch: the work a
	// loop did outlives the loop
	s.mustJSON("DELETE", "/api/loops/alpha?remove_worktree=1", nil, nil)
	if worktrees := gitOut(t, repo, "worktree", "list"); strings.Contains(worktrees, "worktrees/alpha") {
		t.Fatalf("alpha's worktree survived the delete:\n%s", worktrees)
	}
	if branches := gitOut(t, repo, "branch", "--list", "loop/alpha"); !strings.Contains(branches, "loop/alpha") {
		t.Fatal("loop/alpha was deleted with the loop; the branch is the work and must be kept")
	}
	if beta := gitOut(t, repo, "worktree", "list"); !strings.Contains(beta, "loop/beta") {
		t.Fatalf("beta's worktree went with alpha's:\n%s", beta)
	}
	if now := gitOut(t, repo, "rev-parse", "main"); now != mainSHA {
		t.Fatalf("main moved from %s to %s; loops never commit to it", mainSHA, now)
	}
}
