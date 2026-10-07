package loop

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestProjectSlugIsClaudes: claude names a project's directory after its
// path with every character but letters and digits made a hyphen, so a dot
// in the path doubles the hyphen.
func TestProjectSlugIsClaudes(t *testing.T) {
	for dir, want := range map[string]string{
		"/home/loop":                 "-home-loop",
		"/home/fixture/.spool/wt/a1": "-home-fixture--spool-wt-a1",
		"/srv/my_repo":               "-srv-my-repo",
	} {
		if got := projectSlug(dir); got != want {
			t.Errorf("projectSlug(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestAWorktreesMemoryIsItsRepos: claude keys the auto-memory of a session
// run in a worktree by the repo's main checkout, which every worktree of it
// shares; outside a repo, by the workspace itself.
func TestAWorktreesMemoryIsItsRepos(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	repo := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(t.TempDir(), "wt")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", worktree},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo, _ = filepath.EvalSymlinks(repo)
	want := filepath.Join(config, "projects", projectSlug(repo), "memory")
	for _, dir := range []string{repo, worktree} {
		if got := claudeMemoryDir(dir); got != want {
			t.Errorf("claudeMemoryDir(%q) = %q, want the repo's %q", dir, got, want)
		}
	}
	plain := t.TempDir()
	if got, want := claudeMemoryDir(plain), filepath.Join(config, "projects", projectSlug(plain), "memory"); got != want {
		t.Errorf("outside a repo, claudeMemoryDir = %q, want the workspace's %q", got, want)
	}
	if got := claudeMemoryDir(""); got != "" {
		t.Errorf("a loop with no workspace has memory at %q, want none", got)
	}
}

// TestStageMemoryCopiesTheNotes: the staged copy keeps every note at its
// relative path and nothing else, and a loop with no memory stages none.
func TestStageMemoryCopiesTheNotes(t *testing.T) {
	source, target := t.TempDir(), filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(filepath.Join(source, "topic"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"MEMORY.md": "index\n", "topic/note.md": "a note\n"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(source, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := stageMemory(source, target); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"MEMORY.md": "index\n", "topic/note.md": "a note\n"} {
		if got, err := os.ReadFile(filepath.Join(target, name)); err != nil || string(got) != body {
			t.Errorf("staged %s = %q (%v), want %q", name, got, err, body)
		}
	}
	if _, err := os.Lstat(filepath.Join(target, "link.md")); !os.IsNotExist(err) {
		t.Errorf("a symlink was staged: %v", err)
	}

	empty := filepath.Join(t.TempDir(), "memory")
	if err := stageMemory(filepath.Join(t.TempDir(), "absent"), empty); err != nil {
		t.Fatalf("no memory to stage failed: %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Errorf("no memory staged a directory anyway: %v", err)
	}
}

// TestRehomePendingSpansTheRequestNotTheRow: a rehome is pending from its
// latch through its handoff, and not once the move has failed, though the
// row still names it as the last rotation's cause on a bare loop (#632).
func TestRehomePendingSpansTheRequestNotTheRow(t *testing.T) {
	rehome := store.RotationReasonRehome
	bare := store.Loop{Runtime: store.RuntimeBare}
	handingOff := bare
	handingOff.RotatePending, handingOff.RotateReason = true, rehome
	failed := bare
	failed.RotateReason = rehome
	for _, testCase := range []struct {
		name          string
		loop          store.Loop
		rotateAsked   string
		handoffReason string
		want          bool
	}{
		{"nothing asked", bare, "", "", false},
		{"latched", bare, rehome, "", true},
		{"a context rotation latched", bare, store.RotationReasonFill, "", false},
		{"handing off", handingOff, rehome, rehome, true},
		{"the move failed", failed, "", rehome, false},
	} {
		actor := &Actor{loop: testCase.loop, rotateAsked: testCase.rotateAsked, handoffReason: testCase.handoffReason}
		if got := actor.rehomePending(); got != testCase.want {
			t.Errorf("%s: rehomePending = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}
