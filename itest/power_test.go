//go:build integration

package itest

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

var powerVerbs = []string{"restart", "poweroff", "poweron", "recreate"}

func (s *server) power(name, verb string) (int, []byte) {
	s.t.Helper()
	resp, body := s.do("POST", "/api/loops/"+name+"/workstation/"+verb, nil)
	return resp.StatusCode, body
}

// A bare loop's workstation is the operator's host, so there is nothing for
// the power controls to act on and every verb says so rather than pretending.
func TestPowerControlsRejectedOnBare(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("hostloop", nil)

	for _, verb := range powerVerbs {
		code, body := s.power("hostloop", verb)
		if code != 409 {
			t.Fatalf("%s on a bare loop: %d %s, want 409", verb, code, body)
		}
		if !strings.Contains(string(body), "bare") {
			t.Fatalf("%s error should name the runtime: %s", verb, body)
		}
		// the two 409s a client can get differ in what it should do, so
		// they are told apart by a code rather than by parsing prose
		if !strings.Contains(string(body), `"code":"no_workstation"`) {
			t.Fatalf("%s 409 should carry the no_workstation code: %s", verb, body)
		}
	}
	if view := s.loop("hostloop"); !view.WorkstationUp || view.DownReason != "" {
		t.Fatalf("bare loop should still read up with no down reason: %+v", view)
	}
}

// The full power cycle on a real workstation: power off ends the turn and
// leaves the loop calmly off (ticks included), power on brings the same
// session back, and recreate rebuilds the machine with a fresh one.
func TestDockerWorkstationPowerCycle(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	// a short tick interval so the "stays off" assertion has ticks to survive
	s.createLoop("wspower", map[string]any{"tick_interval_sec": 2, "min_wake_sec": 1})
	view := s.loop("wspower")
	cleanupWorkstation(t, view.ID)

	s.message("wspower", "before the switch")
	first := s.waitTurn("wspower", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "before the switch")
	})

	// --- power off: down, calmly, and it stays that way
	if code, body := s.power("wspower", "poweroff"); code != 200 {
		t.Fatalf("poweroff: %d %s", code, body)
	}
	off := s.loop("wspower")
	if off.WorkstationUp || off.DownReason != "powered_off" || off.State != "workstation_off" {
		t.Fatalf("after poweroff: up=%v down_reason=%q state=%q, want down/powered_off/workstation_off",
			off.WorkstationUp, off.DownReason, off.State)
	}
	if running, _ := dockerInspect("{{.State.Running}}", "spool-ws-"+view.ID); running != "false" {
		t.Fatalf("container still running after poweroff: %q", running)
	}

	// ticks are skipped rather than queued, so nothing switches it back on
	s.message("wspower", "while the power is off")
	time.Sleep(6 * time.Second)
	if still := s.loop("wspower"); still.State != "workstation_off" {
		t.Fatalf("a powered-off loop woke by itself: state=%q", still.State)
	}

	// --- power on: same machine, same session, and the queued message lands
	if code, body := s.power("wspower", "poweron"); code != 200 {
		t.Fatalf("poweron: %d %s", code, body)
	}
	on := s.loop("wspower")
	if !on.WorkstationUp || on.DownReason != "" {
		t.Fatalf("after poweron: up=%v down_reason=%q, want up with no reason", on.WorkstationUp, on.DownReason)
	}
	resumed := s.waitTurn("wspower", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "while the power is off")
	})
	if resumed.SessionID != first.SessionID {
		t.Fatalf("power off/on lost the session: %q then %q", first.SessionID, resumed.SessionID)
	}

	// --- recreate: a new machine, and the loop starts a fresh session on it
	if code, body := s.power("wspower", "recreate"); code != 200 {
		t.Fatalf("recreate: %d %s", code, body)
	}
	if view := s.loop("wspower"); !view.WorkstationUp || view.DownReason != "" {
		t.Fatalf("after recreate: up=%v down_reason=%q, want up with no reason", view.WorkstationUp, view.DownReason)
	}
	s.message("wspower", "after the rebuild")
	rebuilt := s.waitTurn("wspower", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "after the rebuild")
	})
	if rebuilt.SessionID == first.SessionID {
		t.Fatalf("recreate kept session %q; it destroys the state that session lived in", first.SessionID)
	}
}

// poweron is documented idempotent, so it must not end a turn running on a
// workstation that is already up — the UI hides the button, but the endpoint
// is the enforcement.
func TestDockerPowerOnLeavesARunningTurnAlone(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsidem", map[string]any{"idle_timeout_sec": 30})
	loopID := s.loop("wsidem").ID
	cleanupWorkstation(t, loopID)

	s.message("wsidem", "wake the workstation")
	s.waitTurn("wsidem", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "wake the workstation")
	})

	// make every later turn take its time, so one is genuinely in flight
	if out, err := exec.Command("docker", "exec", "spool-ws-"+loopID,
		"sh", "-c", "printf '!hang 8\\n' > /home/loop/.fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("scripting fakeclaude: %v (%s)", err, out)
	}

	s.message("wsidem", "slow one")
	time.Sleep(2 * time.Second) // the turn is now in flight inside the workstation

	if code, body := s.power("wsidem", "poweron"); code != 200 {
		t.Fatalf("poweron on a live workstation: %d %s", code, body)
	}
	done := s.waitTurn("wsidem", 60*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "slow one")
	})
	if done.IsError {
		t.Fatal("poweron ate the turn it was running against")
	}
}

// A verb that fails leaves everything as it found it. Here the workstation
// can never come up — its image does not exist — so poweron fails and the
// operator's off-intent must survive: the loop stays calmly off rather than
// flipping to the alert and ticking against a machine that isn't there.
func TestDockerFailedPowerOnKeepsTheOffIntent(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	s.createLoop("wsnoimage", map[string]any{
		"image":             "spool-workstation-itest-does-not-exist",
		"tick_interval_sec": 2,
		"min_wake_sec":      1,
	})
	cleanupWorkstation(t, s.loop("wsnoimage").ID)

	if code, body := s.power("wsnoimage", "poweroff"); code != 200 {
		t.Fatalf("poweroff: %d %s", code, body)
	}
	s.waitState("wsnoimage", "workstation_off", 10*time.Second)

	code, body := s.power("wsnoimage", "poweron")
	if code != 500 {
		t.Fatalf("poweron with a missing image: %d %s, want 500", code, body)
	}
	view := s.loop("wsnoimage")
	if view.State != "workstation_off" || view.DownReason != "powered_off" {
		t.Fatalf("failed poweron discarded the off-intent: state=%q down_reason=%q",
			view.State, view.DownReason)
	}
}
