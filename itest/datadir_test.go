//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"testing"
)

// The unit tests pin what Secure does; this pins that startup actually calls
// it. A data directory left over from an older build, world-readable with a
// database full of bot tokens in it, is private by the time the server is
// answering — and the loop homes underneath it are unreachable to other
// accounts because the directory above them denies traversal (#149).
func TestServerTightensItsDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dataDir, "server.log")
	if err := os.WriteFile(log, []byte("a leaked bot token used to live here"), 0o644); err != nil {
		t.Fatal(err)
	}

	startServer(t, dataDir)

	// The database is the interesting one: it does not exist when the server
	// starts, so its mode is the one sqlite gave it under the ambient umask.
	for _, tc := range []struct {
		path string
		want os.FileMode
	}{
		{dataDir, 0o700},
		{log, 0o600},
		{filepath.Join(dataDir, "spool.db"), 0o600},
		{filepath.Join(dataDir, "spool.db-wal"), 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Errorf("%s mode = %#o, want %#o", filepath.Base(tc.path), got, tc.want)
		}
	}
}
