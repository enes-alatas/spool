// Package gitws manages per-loop git worktrees so loops sharing a repo can't
// clobber each other.
package gitws

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsGitRepo reports whether dir is inside a git work tree.
func IsGitRepo(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Create adds a worktree for loopName at worktreeDir on branch loop/<name>.
// If the branch already exists it is reused (e.g. loop re-created).
func Create(repo, worktreeDir, loopName string) (branch string, err error) {
	branch = "loop/" + loopName
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		return "", err
	}
	// try with a new branch first; fall back to reusing an existing branch
	cmd := exec.Command("git", "-C", repo, "worktree", "add", worktreeDir, "-b", branch)
	if out, err2 := cmd.CombinedOutput(); err2 != nil {
		if strings.Contains(string(out), "already exists") {
			cmd = exec.Command("git", "-C", repo, "worktree", "add", worktreeDir, branch)
			if out2, err3 := cmd.CombinedOutput(); err3 != nil {
				return "", fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(out2)), err3)
			}
		} else {
			return "", fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(out)), err2)
		}
	}
	return branch, nil
}

// Remove deletes a loop's worktree (the branch is kept — work survives).
func Remove(repo, worktreeDir string) error {
	out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", worktreeDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
