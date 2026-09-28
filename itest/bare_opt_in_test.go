//go:build integration

package itest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `--runtime auto` used to mean "docker if you have it, host subprocesses
// otherwise". The fallback was the one outcome a person who wrote `auto`
// cannot have asked for — claude with permissions bypassed under their own
// account — and it announced itself with a log line (#240). Now auto is
// docker or a refusal that names both remedies.
//
// Tier 2 because it is a property of starting the binary: its exit status and
// what it says. DOCKER_HOST points at a closed port, so the daemon is
// unreachable whether or not this machine has one.
func TestAutoRuntimeRefusesToFallBackToBare(t *testing.T) {
	t.Parallel()
	spoolBin := filepath.Join(repoRoot(t), "bin", "spool")
	if _, err := os.Stat(spoolBin); err != nil {
		t.Fatalf("%s missing — run via `make itest`", spoolBin)
	}
	dataDir := t.TempDir()

	// Both ports are the kernel's pick, which --mcp-listen accepts (#344), so
	// the refusal this test sees is the runtime's and not the listeners'.
	cmd := exec.Command(spoolBin,
		"--runtime", "auto",
		"--data-dir", dataDir,
		"--listen", "127.0.0.1:0",
		"--mcp-listen", "127.0.0.1:0",
	)
	cmd.Env = append(os.Environ(), "DOCKER_HOST=tcp://127.0.0.1:1", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()

	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("spool --runtime auto without docker did not exit non-zero: err=%v, output:\n%s", err, out)
	}
	if exit.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1; output:\n%s", exit.ExitCode(), out)
	}
	// Both halves: what to fix, and how to ask for the uncontained runtime on
	// purpose. A refusal that only says "no docker" sends the reader to the
	// flag list to guess.
	for _, want := range []string{"docker", "--runtime bare", "uncontained"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the refusal never mentions %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "spool listening") {
		t.Errorf("the hub started anyway:\n%s", out)
	}
}

// Creating a bare loop is a decision taken at the terminal, by the person who
// started the hub — not one the control room can make for them afterwards. A
// hub started on docker refuses it and says how to allow it.
func controlRoomCannotCreateABareLoopOnADockerHub(t *testing.T) {
	s := startDockerServer(t, t.TempDir())

	resp, body := s.do("POST", "/api/loops", map[string]any{
		"name":              "smuggled",
		"mission":           "integration test loop",
		"runtime":           "bare",
		"tick_interval_sec": 3600,
		"min_wake_sec":      60,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("POST /api/loops runtime=bare = %d, want 400 (%s)", resp.StatusCode, body)
	}
	var errBody struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("refusal is not json: %v (%s)", err, body)
	}
	if errBody.Code != "bare_runtime_not_enabled" {
		t.Errorf("code = %q, want bare_runtime_not_enabled (%s)", errBody.Code, body)
	}
	if !strings.Contains(errBody.Error, "--runtime bare") {
		t.Errorf("the refusal does not say how to allow one: %q", errBody.Error)
	}

	// And the loop really was not created — a refusal issued after the row
	// exists is not a refusal.
	var loops []loopView
	s.mustJSON("GET", "/api/loops", nil, &loops)
	for _, l := range loops {
		if l.Name == "smuggled" {
			t.Error("the loop was created anyway")
		}
	}
}

// The control room decides whether to offer an uncontained loop, and which
// runtime New loop starts on, from what `GET /api/settings` says, so the
// flags the operator typed at the terminal have to survive the trip (#255,
// #258). A form that offered the choice on a hub that refuses it would be
// offering a create that 400s; one that hid it on a hub that allows it would
// hide the escape hatch `--allow-bare` exists to give.
func settingsReportsWhetherBareIsAllowed(t *testing.T) {
	// A bare hub: the shape of a single-machine install, where every loop the
	// room creates is uncontained and the form has to say so.
	t.Run("a bare hub allows one", func(t *testing.T) {
		assertHubRuntime(t, startServer(t, t.TempDir()), true, "bare")
	})

	// The docker cases need a daemon, because `--runtime docker` refuses to
	// boot without one; startDockerServer skips when it is not there.
	t.Run("a docker hub does not", func(t *testing.T) {
		assertHubRuntime(t, startDockerServer(t, t.TempDir()), false, "docker")
	})

	// Allowing bare does not make it the default: a create that names no
	// runtime still gets a workstation.
	t.Run("--allow-bare is the escape hatch on a docker hub", func(t *testing.T) {
		assertHubRuntime(t, startDockerServer(t, t.TempDir(), "--allow-bare"), true, "docker")
	})
}

func assertHubRuntime(t *testing.T, s *server, wantBareAllowed bool, wantDefault string) {
	t.Helper()
	var view struct {
		BareAllowed    bool   `json:"bare_allowed"`
		DefaultRuntime string `json:"default_runtime"`
	}
	s.mustJSON("GET", "/api/settings", nil, &view)
	if view.BareAllowed != wantBareAllowed {
		t.Errorf("bare_allowed = %v, want %v", view.BareAllowed, wantBareAllowed)
	}
	if view.DefaultRuntime != wantDefault {
		t.Errorf("default_runtime = %q, want %q", view.DefaultRuntime, wantDefault)
	}
}
