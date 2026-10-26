package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reap, for a loop with a claude recorded running, asks docker one thing,
// the exec of pkill, and reads its answer: a workstation that is gone,
// stopped or paused has nothing left to reap, nor has one where pkill
// matched nothing, but a daemon it cannot reach is an error (#1134).
func TestReapExecsOnceAndReadsTheRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, exec string
		wantErr    bool
	}{
		{"claude signalled", "exit 0", false},
		{"no claude inside", "exit 1", false},
		{"workstation gone", "echo 'Error response from daemon: No such container: spool-ws-l1' >&2; exit 1", false},
		{"workstation stopped", "echo 'Error response from daemon: container 4f2a is not running' >&2; exit 1", false},
		{"workstation paused", "echo 'Error response from daemon: Container 4f2a is paused, unpause the container before exec' >&2; exit 1", false},
		{"daemon unreachable", "echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock.' >&2; exit 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
			script := "#!/bin/sh\necho \"$*\" >>" + calls + "\n" + tc.exec + "\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			rt := New(Options{Bin: bin})
			err := rt.Reap(context.Background(), "l1", 4242)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Reap: %v, want an error: %v", err, tc.wantErr)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.TrimSpace(string(data)), "exec spool-ws-l1 pkill -TERM -x claude"; got != want {
				t.Errorf("Reap asked docker:\n%s\nwant only %q", got, want)
			}
		})
	}
}

// A loop the hub had no claude running for has nothing to reap, and the
// workstation is not asked: a boot over a hundred idle docker workstations
// would otherwise exec into every one of them (#1134).
func TestReapLeavesAnIdleWorkstationUnasked(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >>"+calls+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New(Options{Bin: bin}).Reap(context.Background(), "l1", 0); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if data, err := os.ReadFile(calls); err == nil {
		t.Errorf("Reap with no claude recorded asked docker:\n%s", data)
	}
}

// A stop whose TERM finds no claude still sends the KILL after the grace:
// claude's exec runs sh until sh execs claude, and a claude that starts
// after the TERM must not run on past the stop.
func TestStopClaudeKillsAfterATermThatFoundNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "docker"), filepath.Join(dir, "calls")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >>"+calls+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := New(Options{Bin: bin}).stopClaude(context.Background(), "spool-ws-l1"); err != nil {
		t.Fatalf("stopClaude: %v", err)
	}
	want := "exec spool-ws-l1 pkill -KILL -x claude"
	for deadline := time.Now().Add(orphanGrace + 5*time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if data, _ := os.ReadFile(calls); strings.Contains(string(data), want) {
			return
		}
	}
	t.Fatalf("no %q within the grace after a TERM that found no claude", want)
}
