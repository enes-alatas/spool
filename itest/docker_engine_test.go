//go:build integration

package itest

import (
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	workstationTestImage = "spool-workstation-itest"
	// The suite's own proxy build. The egress wall is named after its image
	// (ADR-0028), so these tests get their own network and proxy container and
	// never join a real fleet's — nor the docker runtime suite's, which runs
	// beside this one under `make itest` and tags itself
	// `spool-egress-itest-docker` for that reason (#221).
	egressTestImage = "spool-egress-itest"
)

// dockerTestToken is a well-formed placeholder setup-token: the workstation
// runs fakeclaude, which ignores it, but the engine still requires one before
// it will wake a contained loop.
const dockerTestToken = "sk-ant-oat01-itesttoken000000000000000000"

// startDockerServer spawns spool defaulting to docker workstations, on the
// fakeclaude test image with a fast liveness poll; skips when the daemon or
// image is unavailable. It configures the operator setup-token so contained
// loops can wake.
func startDockerServer(t *testing.T, dataDir string, extraArgs ...string) *server {
	t.Helper()
	requireWorkstationImage(t)
	args := append([]string{
		"--runtime", "docker",
		"--workstation-image", workstationTestImage,
		"--egress-image", egressTestImage,
		"--workstation-health-sec", "2",
	}, extraArgs...)
	// A workstation is allowlisted to the MCP port and reaches it over the
	// docker bridge, which has no route to loopback (#238). The hub picks
	// that address itself when told none (#475), so every docker row runs
	// on the listener an operator who names none gets.
	s := startServerOn(t, dataDir, "", args...)
	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": dockerTestToken}, nil)
	return s
}

// TestDockerRows runs every row that drives the real daemon, one at a time.
// The rows share the daemon and more than that: the egress wall is named
// after its image (ADR-0028), so every hub on the suite's image joins one
// proxy and one network. They share nothing with the bare rows, so the
// series as a whole runs in parallel with those (#182). As subtests of one
// parallel test they hold one of the -parallel slots between them; as
// parallel tests queued on a lock, the waiting ones would hold the rest.
func TestDockerRows(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"ControlRoomCannotCreateABareLoopOnADockerHub", controlRoomCannotCreateABareLoopOnADockerHub},
		{"SettingsReportsWhetherBareIsAllowed", settingsReportsWhetherBareIsAllowed},
		{"DockerEgressAllowlist", dockerEgressAllowlist},
		{"DockerWorkstationCannotReachTheAPI", dockerWorkstationCannotReachTheAPI},
		{"DockerEchoTurn", dockerEchoTurn},
		{"DockerScriptedTurn", dockerScriptedTurn},
		{"DockerHookRefusesASharedStateCommand", dockerHookRefusesASharedStateCommand},
		{"DockerWorkstationCustomSpec", dockerWorkstationCustomSpec},
		{"DockerIdleDrainAndResume", dockerIdleDrainAndResume},
		{"DockerOrchestratorRestartReconnects", dockerOrchestratorRestartReconnects},
		{"DockerWorkstationDownSurfacesAndHeals", dockerWorkstationDownSurfacesAndHeals},
		{"DockerLoopDeleteRemovesWorkstation", dockerLoopDeleteRemovesWorkstation},
		{"MixedRuntimeFleet", mixedRuntimeFleet},
		{"DockerWorkstationNeedsClaudeToken", dockerWorkstationNeedsClaudeToken},
		{"DockerWorkstationPowerCycle", dockerWorkstationPowerCycle},
		{"DockerPowerOnLeavesARunningTurnAlone", dockerPowerOnLeavesARunningTurnAlone},
		{"DockerFailedPowerOnKeepsTheOffIntent", dockerFailedPowerOnKeepsTheOffIntent},
		{"DockerDownReasonNamesTheFault", dockerDownReasonNamesTheFault},
	} {
		t.Run(row.name, row.run)
	}
}

func requireWorkstationImage(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("docker daemon not reachable — docker workstation itests skipped")
	}
	for _, image := range []string{workstationTestImage, egressTestImage} {
		if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
			t.Skipf("%s image missing — run via `make itest`", image)
		}
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

func dockerEchoTurn(t *testing.T) {
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

// dockerScriptedTurn: a contained loop can be scripted exactly as a bare
// one is, which tier 2 could not do before — a docker loop's working
// directory is inside its workstation, so the .fakeclaude file never reached
// it and every contained turn could only echo (#117). Without scripting,
// nothing that depends on what a turn *does* — a crash, a hang, a full
// context — could be tested against a workstation at all.
func dockerScriptedTurn(t *testing.T) {
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

// dockerWorkstationCustomSpec pins that a loop's own image and limits —
// not the server defaults — reach docker run. The image is a distinct tag of
// the test image so the assertion can tell the two apart.
func dockerWorkstationCustomSpec(t *testing.T) {
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

// dockerIdleDrainAndResume is the containerized analog of the bare
// idle-reap test: the idle timeout drains the exec via stdin EOF, and the
// next message resumes the same session off the volume-backed home.
func dockerIdleDrainAndResume(t *testing.T) {
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

func dockerOrchestratorRestartReconnects(t *testing.T) {
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

func dockerWorkstationDownSurfacesAndHeals(t *testing.T) {
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

func dockerLoopDeleteRemovesWorkstation(t *testing.T) {
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

// mixedRuntimeFleet pins the per-loop dispatch: an explicit bare loop on
// a docker-default server still runs as a host subprocess.
// A fleet may mix runtimes: the docker default does not force a workstation
// on a loop asked for as bare. Since #240 the uncontained half of that mix is
// opt-in at startup — the property here is per-loop choice, not a hub that
// hands out uncontained loops to whoever asks.
func mixedRuntimeFleet(t *testing.T) {
	s := startDockerServer(t, t.TempDir(), "--allow-bare")
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

// dockerWorkstationNeedsClaudeToken pins the wake-time gate: with no
// operator setup-token a contained loop refuses to wake — it surfaces the
// reason and provisions nothing, rather than execing claude with no login —
// and setting the token lets the same loop run.
func dockerWorkstationNeedsClaudeToken(t *testing.T) {
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
	t.Parallel()
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

// A docker loop on a hub whose loop listener its workstation cannot reach
// could never talk: on an engine running on this machine, the workstation
// comes in at the bridge gateway, so a listener bound to loopback or to any
// other one address refuses it. Creating one is refused, naming the address
// that works, and nothing is stored; a listener on the gateway itself is
// reachable and is not refused (#474).
func TestDockerLoopRefusedOnAnUnreachableLoopListener(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("docker", "network", "inspect", "bridge", "--format", "{{range .IPAM.Config}}{{.Gateway}} {{end}}").Output()
	if err != nil {
		t.Skip("docker daemon not reachable — the bridge has no gateway to check")
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || !localAddress(fields[0]) {
		t.Skipf("the bridge gateway %q is not on this machine: an engine in a VM forwards to loopback itself", out)
	}
	gateway := fields[0]

	refused := map[string]string{"loopback": "127.0.0.1"}
	if other := otherLocalAddress(gateway); other != "" {
		refused["another address of this machine"] = other
	}
	for why, host := range refused {
		t.Run(why, func(t *testing.T) {
			t.Parallel()
			s := startServerOn(t, t.TempDir(), host, "--runtime", "bare")
			resp, body := s.do("POST", "/api/loops", map[string]any{"name": "walled", "mission": "m", "runtime": "docker"})
			if resp.StatusCode != 400 || errorCode(body) != "loop_listener_unreachable" {
				t.Fatalf("a docker loop behind a listener on %s = %d %s, want 400 loop_listener_unreachable", host, resp.StatusCode, body)
			}
			if !strings.Contains(string(body), "--mcp-listen "+gateway+":") {
				t.Errorf("the refusal %s does not name the bridge address %s", body, gateway)
			}
			if _, loops := s.do("GET", "/api/loops", nil); strings.Contains(string(loops), "walled") {
				t.Errorf("the refused loop was stored: %s", loops)
			}
		})
	}
	t.Run("the bridge gateway", func(t *testing.T) {
		t.Parallel()
		s := startServerOn(t, t.TempDir(), gateway, "--runtime", "bare")
		resp, body := s.do("POST", "/api/loops", map[string]any{"name": "reachable", "mission": "m", "runtime": "docker"})
		if resp.StatusCode != 201 {
			t.Fatalf("a docker loop behind a listener on the gateway itself = %d %s, want 201", resp.StatusCode, body)
		}
		// No Claude token is set, so no workstation was provisioned; the
		// delete leaves nothing behind either way.
		s.do("DELETE", "/api/loops/reachable", nil)
	})
}

// An operator who names no loop listener gets one their docker loops can
// reach: on an engine running on this machine, the hub binds the bridge
// gateway, which the network around the machine is not routed to by default,
// rather than loopback, which the workstations cannot reach (#475). The docker rows prove a
// workstation reaches /mcp on it; this pins where it went.
func TestTheLoopListenerBindsTheBridgeWhenNoneIsNamed(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("docker", "network", "inspect", "bridge", "--format", "{{range .IPAM.Config}}{{.Gateway}} {{end}}").Output()
	if err != nil {
		t.Skip("docker daemon not reachable — the bridge has no gateway to bind")
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || !localAddress(fields[0]) {
		t.Skipf("the bridge gateway %q is not on this machine: an engine in a VM forwards to loopback itself", out)
	}
	gateway := fields[0]

	// A hub of bare loops has no workstation to reach it, so it stays on
	// loopback.
	bare := startServerOn(t, t.TempDir(), "", "--runtime", "bare")
	if host, _, _ := net.SplitHostPort(strings.TrimPrefix(bare.mcpURL, "http://")); host != "127.0.0.1" {
		t.Fatalf("a hub of bare loops put its loop listener at %s, want loopback", bare.mcpURL)
	}

	s := startServerOn(t, t.TempDir(), "", "--runtime", "docker")
	if host, _, _ := net.SplitHostPort(strings.TrimPrefix(s.mcpURL, "http://")); host != gateway {
		t.Fatalf("with no --mcp-listen the loop listener is at %s, want the bridge gateway %s", s.mcpURL, gateway)
	}
	resp, body := s.do("POST", "/api/loops", map[string]any{"name": "reachable", "mission": "m", "runtime": "docker"})
	if resp.StatusCode != 201 {
		t.Fatalf("a docker loop on the default loop listener = %d %s, want 201", resp.StatusCode, body)
	}
	s.do("DELETE", "/api/loops/reachable", nil)
}

func localAddress(addr string) bool {
	ip := net.ParseIP(addr)
	addrs, _ := net.InterfaceAddrs()
	for _, ifaceAddr := range addrs {
		if ipNet, ok := ifaceAddr.(*net.IPNet); ok && ip != nil && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// otherLocalAddress is an IPv4 address of this machine that is neither
// loopback nor the gateway, "" when there is none.
func otherLocalAddress(gateway string) string {
	addrs, _ := net.InterfaceAddrs()
	for _, ifaceAddr := range addrs {
		ipNet, ok := ifaceAddr.(*net.IPNet)
		if ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() && ipNet.IP.String() != gateway {
			return ipNet.IP.String()
		}
	}
	return ""
}
