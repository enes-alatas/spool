package datadir

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// A fresh data directory is private from the moment it exists — the tokens go
// in before anyone thinks to check the mode.
func TestSecureCreatesPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := Secure(dir, quietLog()); err != nil {
		t.Fatalf("Secure: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != DirMode {
		t.Fatalf("data dir mode = %#o, want %#o", got, DirMode)
	}
}

// The case that made #149 an incident: the directory and the database already
// exist, world-readable, written by a build that did not care. Startup has to
// fix what it inherits, not only what it creates.
func TestSecureNarrowsWhatItInherits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spool.db", "spool.db-wal", "server.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Secure(dir, quietLog()); err != nil {
		t.Fatalf("Secure: %v", err)
	}
	if got := statPerm(t, dir); got != DirMode {
		t.Errorf("data dir mode = %#o, want %#o", got, DirMode)
	}
	for _, name := range []string{"spool.db", "spool.db-wal", "server.log"} {
		if got := statPerm(t, filepath.Join(dir, name)); got != FileMode {
			t.Errorf("%s mode = %#o, want %#o", name, got, FileMode)
		}
	}
}

// Tightening only ever removes bits. A deployment that chose 0400 for the
// database, or 0500 for the directory, means it; startup must not widen it
// back to the default on the way past.
func TestSecureNeverWidens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "spool.db")
	if err := os.WriteFile(db, []byte("secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := Secure(dir, quietLog()); err != nil {
		t.Fatalf("Secure: %v", err)
	}
	if got := statPerm(t, dir); got != 0o500 {
		t.Errorf("data dir mode = %#o, want it left at 0500", got)
	}
	if got := statPerm(t, db); got != 0o400 {
		t.Errorf("spool.db mode = %#o, want it left at 0400", got)
	}
}

// A file Spool has not written yet is not an error — it will be created with
// the right mode when it appears.
func TestSecureSkipsAbsentFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := Secure(dir, quietLog()); err != nil {
		t.Fatalf("Secure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spool.db")); !os.IsNotExist(err) {
		t.Fatalf("Secure created spool.db: %v", err)
	}
}

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// A file Spool cannot chmod must not stop it starting. The realistic case is
// server.log left root-owned by one `sudo spool`; the test reaches it by
// symlinking to a file owned by root, because chmod follows the link and
// fails with EPERM exactly as it would on the real thing. The directory is
// private either way, which is the reason this is survivable.
func TestSecureSurvivesAFileItCannotChmod(t *testing.T) {
	const root = "/etc/hostname"
	info, err := os.Stat(root)
	if err != nil {
		t.Skipf("need a file this test does not own: %v", err)
	}
	// Two things have to hold, and each fails the test quietly on its own.
	// Ownership, not root-ness: chmod succeeds on anything this euid owns,
	// and in a rootless container it owns /etc/hostname — the wrong guard
	// would let the test chmod a system file before the assertion below
	// could catch it. And something to narrow: if the file were already
	// 0600, tighten returns before it ever calls chmod, and the test would
	// pass green having exercised no error path at all.
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid == uint32(os.Geteuid()) {
		t.Skip("this euid owns /etc/hostname, so every chmod succeeds")
	}
	if info.Mode().Perm()&^FileMode == 0 {
		t.Skip("/etc/hostname is already narrow, so tighten never reaches chmod")
	}
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(dir, "server.log")); err != nil {
		t.Fatal(err)
	}
	if err := Secure(dir, quietLog()); err != nil {
		t.Fatalf("Secure gave up over a file it cannot chmod: %v", err)
	}
	if got := statPerm(t, dir); got != DirMode {
		t.Errorf("data dir mode = %#o, want %#o", got, DirMode)
	}
	if got := statPerm(t, root); got != info.Mode().Perm() {
		t.Fatalf("%s mode changed to %#o", root, got)
	}
}
