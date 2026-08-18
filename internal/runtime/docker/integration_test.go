//go:build integration

// Tier-2 tests for the docker runtime against a real daemon and the
// fakeclaude workstation image (built by `make itest`). They self-skip when
// no daemon is reachable; CI always runs them.
package docker

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

const testImage = "spool-workstation-itest"

func requireDocker(t *testing.T) *Runtime {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("docker daemon not reachable — docker workstation itests skipped")
	}
	if err := exec.Command("docker", "image", "inspect", testImage).Run(); err != nil {
		t.Skipf("%s image missing — run via `make itest`", testImage)
	}
	return New("", testImage, time.Second)
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := crand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// testSpec provisions under a unique loop id and registers PowerOff cleanup
// so failed tests don't leak containers.
func testSpec(t *testing.T, rt *Runtime) runtime.Spec {
	t.Helper()
	loopID := "itest-" + randomHex(6)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = rt.PowerOff(ctx, loopID)
	})
	return runtime.Spec{
		LoopID:    loopID,
		LoopName:  "itest",
		WorkDir:   runtime.WorkstationHome,
		SessionID: "sess-" + randomHex(6),
		MemMB:     512,
		CPUs:      1,
	}
}

func mustEnsure(t *testing.T, rt *Runtime, spec runtime.Spec) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := rt.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}

func mustStart(t *testing.T, rt *Runtime, spec runtime.Spec) runtime.Proc {
	t.Helper()
	proc, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return proc
}

// awaitResult reads events until the first result (echoed by fakeclaude
// inside the container) or fails on close/timeout.
func awaitResult(t *testing.T, proc runtime.Proc, timeout time.Duration) claude.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-proc.Events():
			if !ok {
				t.Fatalf("stdout closed before a result arrived (stderr: %s)", proc.Wait().Stderr)
			}
			if ev.Type == "result" {
				return ev
			}
		case <-deadline:
			t.Fatalf("no result within %s", timeout)
		}
	}
}

// awaitClose drains events until stdout closes, or fails on timeout.
func awaitClose(t *testing.T, proc runtime.Proc, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-proc.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("stdout did not close within %s", timeout)
		}
	}
}

// dockerOut runs a docker CLI query for assertions.
func dockerOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestEnsureCreatesWorkstation(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	name := containerName(spec.LoopID)
	format := `{{.State.Running}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{.HostConfig.RestartPolicy.Name}} {{index .Config.Labels "spool.loop.id"}}`
	out, err := dockerOut(t, "inspect", "--format", format, name)
	if err != nil {
		t.Fatalf("inspect: %v (%s)", err, out)
	}
	want := fmt.Sprintf("true %d %d unless-stopped %s", 512*1024*1024, int64(1e9), spec.LoopID)
	if out != want {
		t.Fatalf("workstation config = %q, want %q", out, want)
	}
	if out, err := dockerOut(t, "volume", "inspect", name); err != nil {
		t.Fatalf("volume missing: %v (%s)", err, out)
	}
}

func TestEnsureIdempotentAndRestartsStopped(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)
	mustEnsure(t, rt, spec) // running: no-op

	name := containerName(spec.LoopID)
	if out, err := dockerOut(t, "stop", "--time", "1", name); err != nil {
		t.Fatalf("stop: %v (%s)", err, out)
	}
	mustEnsure(t, rt, spec)
	out, err := dockerOut(t, "inspect", "--format", "{{.State.Running}}", name)
	if err != nil || out != "true" {
		t.Fatalf("stopped workstation did not come back: %q %v", out, err)
	}
}

// TestStartRunsTurnAndDrainsOnStdinClose pins the riskiest protocol
// assumption: client-side stdin EOF reaches the exec'd claude, which then
// exits 0 — the idle-drain path the engine depends on.
func TestStartRunsTurnAndDrainsOnStdinClose(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)
	proc := mustStart(t, rt, spec)

	if err := proc.Send("hello wall"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	result := awaitResult(t, proc, 30*time.Second)
	if result.Result == nil || result.Result.ResultText != "echo: hello wall" {
		t.Fatalf("unexpected result: %+v", result.Result)
	}

	if err := proc.CloseStdin(); err != nil {
		t.Fatalf("CloseStdin: %v", err)
	}
	awaitClose(t, proc, 30*time.Second)
	if exit := proc.Wait(); exit.Code != 0 {
		t.Fatalf("drain exit = %d, want 0 (stderr: %s)", exit.Code, exit.Stderr)
	}
}

func TestResumeFailurePropagatesThroughExec(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	spec.SessionID = ""
	spec.ResumeID = "sess-never-existed"
	proc := mustStart(t, rt, spec)
	awaitClose(t, proc, 30*time.Second)
	exit := proc.Wait()
	if !claude.IsSessionNotFound(exit) {
		t.Fatalf("want the canonical session-lost exit through the exec; got code %d stderr %q", exit.Code, exit.Stderr)
	}
}

func TestHomePersistsAcrossExecs(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	proc := mustStart(t, rt, spec)
	if err := proc.Send("first"); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, proc, 30*time.Second)
	_ = proc.CloseStdin()
	awaitClose(t, proc, 30*time.Second)

	resume := spec
	resume.SessionID = ""
	resume.ResumeID = spec.SessionID
	proc = mustStart(t, rt, resume)
	if err := proc.Send("second"); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, proc, 30*time.Second); result.Result.IsError {
		t.Fatalf("resume failed: %+v", result.Result)
	}
	_ = proc.CloseStdin()
	awaitClose(t, proc, 30*time.Second)
}

func TestHomePersistsAcrossContainerRecreate(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	proc := mustStart(t, rt, spec)
	if err := proc.Send("before recreate"); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, proc, 30*time.Second)
	_ = proc.CloseStdin()
	awaitClose(t, proc, 30*time.Second)

	// the container dies; the volume — the workstation's home — survives
	if out, err := dockerOut(t, "rm", "--force", containerName(spec.LoopID)); err != nil {
		t.Fatalf("rm: %v (%s)", err, out)
	}
	mustEnsure(t, rt, spec)

	resume := spec
	resume.SessionID = ""
	resume.ResumeID = spec.SessionID
	proc = mustStart(t, rt, resume)
	if err := proc.Send("after recreate"); err != nil {
		t.Fatal(err)
	}
	if result := awaitResult(t, proc, 30*time.Second); result.Result.IsError {
		t.Fatalf("resume after recreate failed: %+v", result.Result)
	}
	_ = proc.CloseStdin()
	awaitClose(t, proc, 30*time.Second)
}

func TestKillTerminatesInnerClaude(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)
	proc := mustStart(t, rt, spec)
	if err := proc.Send("waiting"); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, proc, 30*time.Second)

	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	awaitClose(t, proc, 30*time.Second)
	proc.Wait()
	assertNoClaudeInside(t, containerName(spec.LoopID))
}

func TestReapKillsLeftoverClaude(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)
	proc := mustStart(t, rt, spec)
	if err := proc.Send("orphan me"); err != nil {
		t.Fatal(err)
	}
	awaitResult(t, proc, 30*time.Second)

	// abandon the exec client (a previous orchestrator's leftover) and reap
	if err := rt.Reap(context.Background(), spec.LoopID, 0); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	awaitClose(t, proc, 30*time.Second)
	assertNoClaudeInside(t, containerName(spec.LoopID))
}

func assertNoClaudeInside(t *testing.T, container string) {
	t.Helper()
	deadline := time.Now().Add(orphanGrace + 10*time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("docker", "exec", container, "pgrep", "-x", "claude").Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return // pgrep: no process matched
			}
			t.Fatalf("pgrep inside %s: %v", container, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("claude still running inside %s", container)
}

func TestPowerOffRemovesContainerAndVolume(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	if err := rt.PowerOff(context.Background(), spec.LoopID); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
	name := containerName(spec.LoopID)
	if _, err := dockerOut(t, "inspect", name); err == nil {
		t.Fatal("container survived PowerOff")
	}
	if _, err := dockerOut(t, "volume", "inspect", name); err == nil {
		t.Fatal("volume survived PowerOff")
	}
	if err := rt.PowerOff(context.Background(), spec.LoopID); err != nil {
		t.Fatalf("second PowerOff must be a no-op: %v", err)
	}
}

func TestHealthReflectsContainerState(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	awaitHealth(t, rt, spec.LoopID, true, "")
	if out, err := dockerOut(t, "stop", "--time", "1", containerName(spec.LoopID)); err != nil {
		t.Fatalf("stop: %v (%s)", err, out)
	}
	awaitHealth(t, rt, spec.LoopID, false, "Exited")

	if health, err := rt.Health(context.Background(), "no-such-loop"); err != nil || health.Up || health.Detail != "workstation not found" {
		t.Fatalf("unknown loop health = %+v, %v", health, err)
	}
}

// awaitHealth polls past the runtime's cache TTL until the wanted liveness
// shows up.
func awaitHealth(t *testing.T, rt *Runtime, loopID string, wantUp bool, wantDetail string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last runtime.Health
	for time.Now().Before(deadline) {
		health, err := rt.Health(context.Background(), loopID)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		last = health
		if health.Up == wantUp && strings.Contains(health.Detail, wantDetail) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("health never reached up=%v detail~%q; last %+v", wantUp, wantDetail, last)
}
