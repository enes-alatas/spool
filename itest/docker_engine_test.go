//go:build integration

package itest

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

const workstationTestImage = "spool-workstation-itest"

// dockerTestToken is a well-formed placeholder setup-token: the workstation
// runs fakeclaude, which ignores it, but the engine still requires one before
// it will wake a contained loop.
const dockerTestToken = "sk-ant-oat01-itesttoken000000000000000000"

// startDockerServer spawns spool defaulting to docker workstations, on the
// fakeclaude test image with a fast liveness poll; skips when the daemon or
// image is unavailable. It configures the operator setup-token so contained
// loops can wake.
func startDockerServer(t *testing.T, dataDir string) *server {
	t.Helper()
	requireWorkstationImage(t)
	s := startServerArgs(t, dataDir,
		"--runtime", "docker",
		"--workstation-image", workstationTestImage,
		"--workstation-health-sec", "2",
	)
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": dockerTestToken}, nil)
	return s
}

func requireWorkstationImage(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("docker daemon not reachable — docker workstation itests skipped")
	}
	if err := exec.Command("docker", "image", "inspect", workstationTestImage).Run(); err != nil {
		t.Skipf("%s image missing — run via `make itest`", workstationTestImage)
	}
}

// cleanupWorkstation removes a loop's container and volume directly — the
// server may already be gone when cleanups run, so this never goes through
// the API.
func cleanupWorkstation(t *testing.T, loopID string) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "--force", "spool-ws-"+loopID).Run()
		_ = exec.Command("docker", "volume", "rm", "--force", "spool-ws-"+loopID).Run()
	})
}

func dockerInspect(format, name string) (string, error) {
	out, err := exec.Command("docker", "inspect", "--format", format, name).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestDockerEchoTurn(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsecho", nil)
	view := s.loop("wsecho")
	cleanupWorkstation(t, view.ID)
	if view.Runtime != "docker" {
		t.Fatalf("loop runtime = %q, want docker", view.Runtime)
	}
	if view.WorkspacePath != "/home/loop" {
		t.Fatalf("workspace path = %q, want the workstation home", view.WorkspacePath)
	}

	s.message("wsecho", "ping through the wall")
	s.waitTurn("wsecho", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "ping through the wall")
	})
	running, err := dockerInspect("{{.State.Running}}", "spool-ws-"+view.ID)
	if err != nil || running != "true" {
		t.Fatalf("workstation not running after the turn: %q %v", running, err)
	}
	// The API defaults (4096 MB, 2 CPUs) must reach the container — pins the
	// httpapi → store → actor → runtime plumbing, not just runArgv.
	limits, err := dockerInspect("{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}", "spool-ws-"+view.ID)
	if err != nil || limits != "4294967296 2000000000" {
		t.Fatalf("workstation limits = %q, want the API defaults \"4294967296 2000000000\" (%v)", limits, err)
	}
}

// TestDockerScriptedTurn: a contained loop can be scripted exactly as a bare
// one is, which tier 2 could not do before — a docker loop's working
// directory is inside its workstation, so the .fakeclaude file never reached
// it and every contained turn could only echo (#117). Without scripting,
// nothing that depends on what a turn *does* — a crash, a hang, a full
// context — could be tested against a workstation at all.
func TestDockerScriptedTurn(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsscript", nil)
	cleanupWorkstation(t, s.loop("wsscript").ID)
	// one line, so it answers every turn from here on rather than depending
	// on which turn of the session this is; the directive reports a context
	// fill well below the arm threshold, so nothing rotates underneath the test
	s.scriptLoop("wsscript", "!ctx 20000 scripted, not echoed\n")

	s.message("wsscript", "say something of your own")
	answered := s.waitTurn("wsscript", 90*time.Second, func(tr turn) bool {
		return tr.Trigger == "message" && strings.Contains(tr.ResultText, "scripted, not echoed")
	})
	if strings.Contains(answered.ResultText, "say something of your own") {
		t.Fatalf("the contained turn echoed instead of following its script:\n%s", answered.ResultText)
	}
	// and the directives cross with it: what the turn reported about itself
	// is the script's doing, not the fake's default
	if answered.ContextTokens != 20000 {
		t.Fatalf("scripted context = %d tokens, want the directive's 20000", answered.ContextTokens)
	}
}

// TestDockerWorkstationCustomSpec pins that a loop's own image and limits —
// not the server defaults — reach docker run. The image is a distinct tag of
// the test image so the assertion can tell the two apart.
func TestDockerWorkstationCustomSpec(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	altImage := workstationTestImage + "-alt"
	if err := exec.Command("docker", "tag", workstationTestImage, altImage).Run(); err != nil {
		t.Fatalf("tag: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", altImage).Run() })

	s.createLoop("wsspec", map[string]any{"image": altImage, "mem_mb": 512, "cpus": 1.5})
	loopID := s.loop("wsspec").ID
	cleanupWorkstation(t, loopID)

	s.message("wsspec", "sized to order")
	s.waitTurn("wsspec", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "sized to order") })

	got, err := dockerInspect("{{.Config.Image}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}", "spool-ws-"+loopID)
	want := altImage + " 536870912 1500000000"
	if err != nil || got != want {
		t.Fatalf("workstation spec = %q, want %q (%v)", got, want, err)
	}
}

// TestDockerIdleDrainAndResume is the containerized analog of the bare
// idle-reap test: the idle timeout drains the exec via stdin EOF, and the
// next message resumes the same session off the volume-backed home.
func TestDockerIdleDrainAndResume(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsidle", nil)
	cleanupWorkstation(t, s.loop("wsidle").ID)

	s.message("wsidle", "one")
	s.waitTurn("wsidle", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "one") })
	s.waitState("wsidle", "asleep", 30*time.Second)

	s.message("wsidle", "two")
	s.waitTurn("wsidle", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "two") })
	if ids := sessionIDs(s.turns("wsidle")); len(ids) != 1 {
		t.Fatalf("want one resumed session, got %d: %v", len(ids), ids)
	}
}

func TestDockerOrchestratorRestartReconnects(t *testing.T) {
	dataDir := t.TempDir()
	s := startDockerServer(t, dataDir)
	s.createLoop("wsrestart", nil)
	loopID := s.loop("wsrestart").ID
	cleanupWorkstation(t, loopID)

	s.message("wsrestart", "before restart")
	s.waitTurn("wsrestart", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "before restart") })
	containerID, err := dockerInspect("{{.Id}}", "spool-ws-"+loopID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	s.stop()
	s = startDockerServer(t, dataDir)
	s.message("wsrestart", "after restart")
	s.waitTurn("wsrestart", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "after restart") })

	if after, err := dockerInspect("{{.Id}}", "spool-ws-"+loopID); err != nil || after != containerID {
		t.Fatalf("boot must reconnect to the standing workstation, not recreate it (%q → %q, %v)", containerID, after, err)
	}
	if ids := sessionIDs(s.turns("wsrestart")); len(ids) != 1 {
		t.Fatalf("want the session resumed across the restart, got %d sessions: %v", len(ids), ids)
	}
}

func TestDockerWorkstationDownSurfacesAndHeals(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsdown", nil)
	loopID := s.loop("wsdown").ID
	cleanupWorkstation(t, loopID)

	s.message("wsdown", "provision")
	s.waitTurn("wsdown", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "provision") })
	s.waitState("wsdown", "asleep", 30*time.Second)

	// the workstation dies while the loop sleeps; the watch must notice
	if err := exec.Command("docker", "rm", "--force", "spool-ws-"+loopID).Run(); err != nil {
		t.Fatalf("rm: %v", err)
	}
	s.waitState("wsdown", "workstation_down", 30*time.Second)
	if view := s.loop("wsdown"); view.WorkstationUp {
		t.Fatalf("view still reports the workstation up: %+v", view)
	}

	// a wake self-heals: Ensure recreates around the surviving volume
	s.message("wsdown", "heal")
	s.waitTurn("wsdown", 120*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "heal") })
	s.waitState("wsdown", "asleep", 30*time.Second)
	if ids := sessionIDs(s.turns("wsdown")); len(ids) != 1 {
		t.Fatalf("the volume kept the session; want 1 session, got %d: %v", len(ids), ids)
	}
}

func TestDockerLoopDeleteRemovesWorkstation(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsgone", nil)
	loopID := s.loop("wsgone").ID
	cleanupWorkstation(t, loopID)

	s.message("wsgone", "hello")
	s.waitTurn("wsgone", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "hello") })

	s.mustJSON("DELETE", "/api/loops/wsgone", nil, nil)
	if _, err := dockerInspect("{{.Id}}", "spool-ws-"+loopID); err == nil {
		t.Fatal("container survived loop deletion")
	}
	if err := exec.Command("docker", "volume", "inspect", "spool-ws-"+loopID).Run(); err == nil {
		t.Fatal("volume survived loop deletion")
	}
}

// TestMixedRuntimeFleet pins the per-loop dispatch: an explicit bare loop on
// a docker-default server still runs as a host subprocess.
func TestMixedRuntimeFleet(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("stillbare", map[string]any{"runtime": "bare"})
	view := s.loop("stillbare")
	if view.Runtime != "bare" {
		t.Fatalf("loop runtime = %q, want bare", view.Runtime)
	}
	s.message("stillbare", "hi")
	s.waitTurn("stillbare", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "hi") })
	if err := exec.Command("docker", "inspect", "spool-ws-"+view.ID).Run(); err == nil {
		t.Fatal("a bare loop must not get a workstation")
	}
}

// TestDockerWorkstationNeedsClaudeToken pins the wake-time gate: with no
// operator setup-token a contained loop refuses to wake — it surfaces the
// reason and provisions nothing, rather than execing claude with no login —
// and setting the token lets the same loop run.
func TestDockerWorkstationNeedsClaudeToken(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": ""}, nil)

	s.createLoop("wsnotoken", nil)
	loopID := s.loop("wsnotoken").ID
	cleanupWorkstation(t, loopID)

	s.message("wsnotoken", "should not run")
	s.waitState("wsnotoken", "workstation_down", 30*time.Second)
	view := s.loop("wsnotoken")
	if view.WorkstationUp {
		t.Fatalf("workstation reported up without a token: %+v", view)
	}
	if !strings.Contains(view.WorkstationDetail, "Claude token not configured") {
		t.Fatalf("detail %q does not name the missing token", view.WorkstationDetail)
	}
	if err := exec.Command("docker", "inspect", "spool-ws-"+loopID).Run(); err == nil {
		t.Fatal("a workstation was provisioned despite the missing token")
	}
	if done := s.completed("wsnotoken"); len(done) != 0 {
		t.Fatalf("a turn ran without a token: %s", dump(done))
	}

	// once the operator sets the token, the same loop wakes and runs
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": dockerTestToken}, nil)
	s.message("wsnotoken", "now with a token")
	s.waitTurn("wsnotoken", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "now with a token")
	})
}

// TestCreateLoopRuntimeValidation needs no daemon: every case fails before
// the availability probe, so it runs on every machine.
func TestCreateLoopRuntimeValidation(t *testing.T) {
	s := startServer(t, t.TempDir())
	cases := []struct {
		name string
		req  map[string]any
		want string
	}{
		{"unknown runtime", map[string]any{"runtime": "microvm"}, "runtime must be"},
		{"docker with workspace", map[string]any{"runtime": "docker", "workspace_path": "/tmp"}, "workspace_path applies to bare"},
		{"docker mem too small", map[string]any{"runtime": "docker", "mem_mb": 100}, "mem_mb must be"},
		{"docker cpus too big", map[string]any{"runtime": "docker", "cpus": 999}, "cpus must be"},
		{"bare with image", map[string]any{"image": "custom"}, "docker loops only"},
		{"bare with limits", map[string]any{"mem_mb": 1024}, "docker loops only"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			req := map[string]any{"name": "invalid", "mission": "m"}
			for key, value := range testCase.req {
				req[key] = value
			}
			resp, body := s.do("POST", "/api/loops", req)
			if resp.StatusCode != 400 {
				t.Fatalf("status = %d, want 400 (%s)", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), testCase.want) {
				t.Fatalf("error %q does not name the problem (%s)", body, testCase.want)
			}
		})
	}
}
