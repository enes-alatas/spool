package bare

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
)

// stubClaude writes a shell script standing in for the claude binary: it
// touches ready once its signal handler is installed, records a SIGTERM in
// signalled, and otherwise sits still.
func stubClaude(t *testing.T, ready, signalled string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ntrap 'echo term > " + signalled + "; exit 0' TERM\n" +
		"echo ready > " + ready + "\nsleep 30 &\nwait\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// waitFor blocks until path exists, failing the test if it never does.
func waitFor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if exists(t, path) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// Start's ctx governs the spawn attempt only. Cancelling it must not reach
// the process — the wake path hands in a context that outlives the turn, and
// a kill-on-cancel watcher would leak a goroutine per wake.
func TestStartIgnoresContextCancellation(t *testing.T) {
	dir := t.TempDir()
	ready, signalled := filepath.Join(dir, "ready"), filepath.Join(dir, "signalled")
	host := New(stubClaude(t, ready, signalled))

	ctx, cancel := context.WithCancel(context.Background())
	proc, err := host.Start(ctx, runtime.Spec{WorkDir: t.TempDir(), SessionID: "11111111-1111-1111-1111-111111111111"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Kill() })

	waitFor(t, ready)
	cancel()
	time.Sleep(250 * time.Millisecond)
	if exists(t, signalled) {
		t.Fatal("context cancellation killed the process; Start must not watch ctx")
	}

	// Teardown still works — it goes through Kill, not the context.
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	proc.Wait()
	if !exists(t, signalled) {
		t.Fatal("Kill did not signal the process")
	}
}
