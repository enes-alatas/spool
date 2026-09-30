package bare

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
)

// A bare loop may send only a file inside its own working directory (#123,
// operator decision): the host holds the operator's keys and the hub's
// data beside it. Every way out is tried here, and each must be refused.
func TestGetFileReadsOnlyInsideTheWorkspace(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{workspace, outside, filepath.Join(workspace, "sub")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(workspace, name)); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(workspace, "notes.txt"), "mine")
	write(filepath.Join(workspace, "sub", "deep.txt"), "deep")
	write(filepath.Join(outside, "secret.txt"), "not mine")
	link(filepath.Join(outside, "secret.txt"), "file-out")     // a symlink to a file outside
	link(outside, "dir-out")                                   // a symlink to a directory outside
	link("../outside/secret.txt", "relative-out")              // a relative symlink that climbs out
	link("notes.txt", "link-in")                               // a relative symlink that stays inside
	link(filepath.Join(workspace, "notes.txt"), "absolute-in") // an absolute one, which os.Root never follows
	link("missing.txt", "dangling-in")                         // a symlink to nothing
	if err := os.Link(filepath.Join(outside, "secret.txt"), filepath.Join(workspace, "hard-out")); err != nil {
		t.Fatal(err) // a hard link to a file outside: os.Root sees only a name inside
	}
	big, err := os.Create(filepath.Join(workspace, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(101); err != nil {
		t.Fatal(err)
	}
	big.Close()

	host := New("claude")
	const limit = 100
	for _, c := range []struct {
		path string
		want string // the body, when the read is allowed
		err  error
	}{
		{path: "notes.txt", want: "mine"},
		{path: "./sub/../notes.txt", want: "mine"},
		{path: filepath.Join(workspace, "notes.txt"), want: "mine"},
		{path: "sub/deep.txt", want: "deep"},
		{path: "link-in", want: "mine"},

		{path: "../outside/secret.txt", err: runtime.ErrNotOwned},
		{path: "sub/../../outside/secret.txt", err: runtime.ErrNotOwned},
		{path: filepath.Join(outside, "secret.txt"), err: runtime.ErrNotOwned},
		{path: filepath.Join(workspace, "..", "outside", "secret.txt"), err: runtime.ErrNotOwned},
		{path: "/etc/passwd", err: runtime.ErrNotOwned},
		{path: "file-out", err: runtime.ErrNotOwned},
		{path: "dir-out/secret.txt", err: runtime.ErrNotOwned},
		{path: "relative-out", err: runtime.ErrNotOwned},
		{path: "absolute-in", err: runtime.ErrNotOwned},
		{path: "hard-out", err: runtime.ErrNotOwned},
		{path: filepath.Join(workspace, "file-out"), err: runtime.ErrNotOwned},
		{path: "", err: runtime.ErrNotOwned},
		{path: "..", err: runtime.ErrNotOwned},

		{path: "missing.txt", err: runtime.ErrNoSuchFile},
		{path: "dangling-in", err: runtime.ErrNoSuchFile},
		{path: "sub", err: runtime.ErrNotAFile},
		{path: ".", err: runtime.ErrNotAFile},
		{path: "big.bin", err: runtime.ErrFileTooLarge},
	} {
		body, err := host.GetFile(t.Context(), "loop", workspace, c.path, limit)
		if c.err != nil {
			if !errors.Is(err, c.err) {
				t.Errorf("%q: err %v, want %v", c.path, err, c.err)
			}
			if body != nil {
				t.Errorf("%q: refused, yet read %q", c.path, body)
			}
			continue
		}
		if err != nil || string(body) != c.want {
			t.Errorf("%q: read %q, %v; want %q", c.path, body, err, c.want)
		}
	}
}

// A workspace the operator pointed at a directory that holds the hub's
// data, a home directory say, sends nothing at all: os.Root keeps a read
// inside the workspace, not out of a directory within it.
func TestGetFileRefusesAWorkspaceHoldingTheHubData(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, ".spool")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, "notes.txt"), filepath.Join(data, "spool.db")} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host := New("claude")
	host.KeepOut(data)
	for _, path := range []string{"notes.txt", ".spool/spool.db"} {
		if _, err := host.GetFile(t.Context(), "loop", home, path, 100); !errors.Is(err, runtime.ErrNotOwned) {
			t.Errorf("%q from a workspace holding the hub data: %v, want ErrNotOwned", path, err)
		}
	}
	// The data dir as the workspace itself, or a workspace inside it (a
	// loop's home or worktree), is refused, and allowed, likewise.
	if _, err := host.GetFile(t.Context(), "loop", data, "spool.db", 100); !errors.Is(err, runtime.ErrNotOwned) {
		t.Errorf("the data dir as the workspace: %v, want ErrNotOwned", err)
	}
	inner := filepath.Join(data, "homes", "aster")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "out.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := host.GetFile(t.Context(), "loop", inner, "out.txt", 100); err != nil || string(body) != "ok" {
		t.Errorf("a workspace inside the data dir: %q, %v", body, err)
	}
	if _, err := host.GetFile(t.Context(), "loop", inner, "../../spool.db", 100); !errors.Is(err, runtime.ErrNotOwned) {
		t.Errorf("climbing out of a home in the data dir: %v, want ErrNotOwned", err)
	}
}
