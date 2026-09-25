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

const (
	testImage = "spool-workstation-itest"
	// A tag of this suite's own, not the one itest/ uses. The egress wall is
	// named after its image (ADR-0028), so a shared tag means a single
	// `spool-egress-itest-proxy` for both suites — and they do not merely
	// share it. The spec hash covers the allowlist, which carries each
	// suite's own hub port, so each reads the other's proxy as misconfigured,
	// force-removes it and builds its own. Whichever was mid-provision when
	// the other's removal landed then failed against a container that had
	// stopped existing (#221). Two tags, two walls, no race. The Makefile
	// tags this from the same build, so the image is byte-identical.
	testEgressImage = "spool-egress-itest-docker"
)

func requireDocker(t *testing.T) *Runtime {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("docker daemon not reachable — docker workstation itests skipped")
	}
	for _, image := range []string{testImage, testEgressImage} {
		if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
			t.Skipf("%s image missing — run via `make itest`", image)
		}
	}
	// The suite's own proxy image, so these workstations are provisioned
	// behind a wall of their own rather than a real fleet's (ADR-0028).
	return New(Options{
		DefaultImage: testImage,
		EgressImage:  testEgressImage,
		HealthTTL:    time.Second,
	})
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := crand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// testSpec provisions under a unique loop id and registers Destroy cleanup
// so failed tests don't leak containers.
func testSpec(t *testing.T, rt *Runtime) runtime.Spec {
	t.Helper()
	loopID := "itest-" + randomHex(6)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = rt.Destroy(ctx, loopID)
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

func TestDestroyRemovesContainerAndVolume(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)

	if err := rt.Destroy(context.Background(), spec.LoopID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	name := containerName(spec.LoopID)
	if _, err := dockerOut(t, "inspect", name); err == nil {
		t.Fatal("container survived Destroy")
	}
	if _, err := dockerOut(t, "volume", "inspect", name); err == nil {
		t.Fatal("volume survived Destroy")
	}
	if err := rt.Destroy(context.Background(), spec.LoopID); err != nil {
		t.Fatalf("second Destroy must be a no-op: %v", err)
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

// Halt stops the workstation and keeps it: the container is still there,
// stopped, with its volume intact, and Ensure brings it back.
func TestHaltStopsWithoutDestroying(t *testing.T) {
	rt := requireDocker(t)
	spec := testSpec(t, rt)
	mustEnsure(t, rt, spec)
	name := containerName(spec.LoopID)

	if err := rt.Halt(context.Background(), spec.LoopID); err != nil {
		t.Fatalf("Halt: %v", err)
	}
	if state, err := dockerOut(t, "inspect", "--format", "{{.State.Running}}", name); err != nil || strings.TrimSpace(state) != "false" {
		t.Fatalf("container running after Halt: %q %v", state, err)
	}
	if _, err := dockerOut(t, "volume", "inspect", name); err != nil {
		t.Fatalf("volume gone after Halt: %v", err)
	}

	mustEnsure(t, rt, spec)
	if state, err := dockerOut(t, "inspect", "--format", "{{.State.Running}}", name); err != nil || strings.TrimSpace(state) != "true" {
		t.Fatalf("container not running after Ensure: %q %v", state, err)
	}
	if err := rt.Halt(context.Background(), "itest-absent-"+randomHex(4)); err != nil {
		t.Fatalf("Halt of a workstation that isn't there must be a no-op: %v", err)
	}
}

// A resolution run is a throwaway container with no network, removed when
// its init has been read (ADR-0033).
func TestResolveModelRunsInAThrowawayContainerWithNoNetwork(t *testing.T) {
	rt := requireDocker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Hubs the itest suite stopped mid-resolution may have left containers
	// of their own; this run must add none.
	before := resolveContainers(t)

	resolved, err := rt.ResolveModel(ctx, "opus")
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if resolved != "claude-opus-5-5" {
		t.Fatalf("resolved = %q, want what the image's claude reported at init", resolved)
	}
	for name := range resolveContainers(t) {
		if !before[name] {
			t.Fatalf("resolution container %s left behind", name)
		}
	}
}

func resolveContainers(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-a", "--filter", "label=spool.resolve=1", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, name := range strings.Fields(string(out)) {
		names[name] = true
	}
	return names
}
