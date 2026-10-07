//go:build integration

package itest

import (
	"database/sql"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remembered is the note a rehomed loop's auto-memory holds on the host.
const remembered = "the fixture loop remembers this\n"

// startRehomable starts a docker hub that allows bare loops, and creates the
// bare loop name over a workspace whose auto-memory holds remembered. The
// loop has run its first turn, so it has a session. It returns the hub and
// the loop's id.
func startRehomable(t *testing.T, dataDir, name string) (*server, string) {
	t.Helper()
	s := startDockerServer(t, dataDir, "--allow-bare")
	workspace := t.TempDir()
	s.createLoop(name, map[string]any{"runtime": "bare", "workspace_path": workspace})
	loopID := s.loop(name).ID
	cleanupWorkstation(t, loopID)

	// the auto-memory claude keeps for a session run in the workspace
	memory := filepath.Join(claudeConfigDir(dataDir), "projects", slug(workspace), "memory")
	if err := os.MkdirAll(memory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memory, "MEMORY.md"), []byte(remembered), 0o600); err != nil {
		t.Fatal(err)
	}
	s.message(name, "before the move")
	s.waitTurn(name, 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "before the move") })
	return s, loopID
}

// assertRehomed checks the loop runs in its workstation: the row is docker
// at the workstation's home, a turn runs there and its first one starts
// from want, the host's auto-memory is in the container, and the staged
// copy is gone.
func assertRehomed(t *testing.T, s *server, name, loopID, want string) {
	t.Helper()
	eventually(t, "the loop turns docker", func() bool { return s.loop(name).Runtime == "docker" })
	if view := s.loop(name); view.WorkspacePath != "/home/loop" {
		t.Fatalf("workspace = %q after the move, want the workstation's home", view.WorkspacePath)
	}
	s.message(name, "after the move")
	tn := s.waitTurn(name, 60*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "after the move") })
	if !strings.Contains(tn.ResultText, want) {
		t.Fatalf("the first turn in the workstation does not start from %q: %s", want, tn.ResultText)
	}
	out, err := exec.Command("docker", "exec", "spool-ws-"+loopID,
		"cat", "/home/loop/.claude/projects/-home-loop/memory/MEMORY.md").CombinedOutput()
	if err != nil || string(out) != remembered {
		t.Fatalf("the workstation's auto-memory = %q (%v), want the host's", out, err)
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "rehome", loopID)); !os.IsNotExist(err) {
		t.Fatalf("the staged memory outlived its carry: %v", err)
	}
}

// bareLoopRehomesIntoAWorkstation pins the rehome (#624): a bare loop with a
// session is asked for a handoff note that says it is moving, comes back as
// a docker loop whose fresh session starts in the workstation with that
// note, and finds its auto-memory there. A docker loop cannot be rehomed.
func bareLoopRehomesIntoAWorkstation(t *testing.T) {
	s, loopID := startRehomable(t, t.TempDir(), "mover")
	workspace := s.loop("mover").WorkspacePath

	var answer struct {
		Rehoming   bool     `json:"rehoming"`
		LeftBehind string   `json:"left_behind"`
		NotCarried []string `json:"not_carried"`
	}
	s.mustJSON("POST", "/api/loops/mover/rehome", nil, &answer)
	if !answer.Rehoming || answer.LeftBehind != workspace || len(answer.NotCarried) == 0 {
		t.Fatalf("rehome answered %+v, want the promise, the workspace left behind and what is not carried", answer)
	}

	// the handoff turn is told why its session ends
	s.waitTurn("mover", 30*time.Second, func(tn turn) bool {
		return tn.Trigger == "rotation" && strings.Contains(tn.ResultText, "docker\nworkstation")
	})
	assertRehomed(t, s, "mover", loopID, "you were moved into your own docker workstation")

	// one way only
	if resp, body := s.do("POST", "/api/loops/mover/rehome", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("rehoming a docker loop answered %d %s, want 409", resp.StatusCode, body)
	}
}

// bareLoopWithNoSessionRehomesAtOnce: a loop with no session has no handoff
// to write, so the move lands before the answer does, and the loop's next
// turn runs in the workstation.
func bareLoopWithNoSessionRehomesAtOnce(t *testing.T) {
	dataDir := t.TempDir()
	s, loopID := startRehomable(t, dataDir, "unsessioned")
	// the session the first turn made, forgotten while the hub is down, as
	// a workstation recreate or a failed resume leaves a loop
	s.stop()
	setLoopColumns(t, dataDir, loopID, `current_session_id=''`)
	s = startDockerServer(t, dataDir, "--allow-bare")

	s.mustJSON("POST", "/api/loops/unsessioned/rehome", nil, nil)
	if view := s.loop("unsessioned"); view.Runtime != "docker" {
		t.Fatalf("a loop with no session is %q once its rehome is answered, want docker", view.Runtime)
	}
	for _, tn := range s.turns("unsessioned") {
		if tn.Trigger == "rotation" {
			t.Fatalf("a handoff turn ran with no session to hand off from: %s", dump(tn))
		}
	}
	assertRehomed(t, s, "unsessioned", loopID, "after the move")
}

// aRestartFinishesARehome: a hub that goes down while a rehome's handoff is
// in flight finishes the move at boot. The state is the one the handoff
// turn records before it runs: the rotation pending with its reason, and
// here the note it wrote.
func aRestartFinishesARehome(t *testing.T) {
	dataDir := t.TempDir()
	s, loopID := startRehomable(t, dataDir, "interrupted")
	s.stop()
	setLoopColumns(t, dataDir, loopID, `rotate_pending=1, rotate_reason='rehome', handoff_note='fixture handoff: PR 7 is pushed'`)
	s = startDockerServer(t, dataDir, "--allow-bare")

	assertRehomed(t, s, "interrupted", loopID, "written on that machine:\nfixture handoff: PR 7 is pushed")
}

// aRehomedLoopIsNotProvisionedUntilItWakes (#639): a bare loop that has
// run turns, rehomed, has no workstation until its next wake builds one.
// Until then its health reads not_provisioned with no alert, across a
// restart too, where its host turns must not count as the workstation
// having been up. The next wake builds it and runs a turn there.
func aRehomedLoopIsNotProvisionedUntilItWakes(t *testing.T) {
	dataDir := t.TempDir()
	s, loopID := startRehomable(t, dataDir, "unbuilt")
	// no session, so the move lands at once and nothing wakes the loop
	s.stop()
	setLoopColumns(t, dataDir, loopID, `current_session_id=''`)
	s = startDockerServer(t, dataDir, "--allow-bare")
	s.mustJSON("POST", "/api/loops/unbuilt/rehome", nil, nil)

	quiet := func(when string) {
		t.Helper()
		within(t, 20*time.Second, "the moved loop reads not_provisioned "+when, func() bool {
			return s.loop("unbuilt").DownReason == "not_provisioned"
		})
		// a few more polls, at --workstation-health-sec 2, to catch a
		// later one reading the unbuilt machine as lost
		time.Sleep(5 * time.Second)
		if view := s.loop("unbuilt"); view.DownReason != "not_provisioned" || view.State == "workstation_down" {
			t.Fatalf("%s: down_reason=%q state=%q, want not_provisioned and no alert", when, view.DownReason, view.State)
		}
		for _, e := range s.eventsQuery("unbuilt", "limit=500") {
			if e.Type == "spool" && e.Subtype == "workstation_down" {
				t.Fatalf("%s: a workstation_down alert was raised: %s", when, e.Payload)
			}
		}
	}
	quiet("after the move")
	s.stop()
	s = startDockerServer(t, dataDir, "--allow-bare")
	quiet("after a restart")

	assertRehomed(t, s, "unbuilt", loopID, "after the move")
}

// aPendingRehomeShowsOnTheLoop: the loop view says a rehome is pending from
// its 202 until the move lands, so a page read again in between shows it
// (#632). A paused loop holds the request until it is resumed. A restart
// before the handoff turn begins loses the request, and the view says so.
func aPendingRehomeShowsOnTheLoop(t *testing.T) {
	dataDir := t.TempDir()
	s, loopID := startRehomable(t, dataDir, "waiting")
	if s.loop("waiting").Rehoming {
		t.Fatal("rehoming before any rehome was asked for")
	}
	s.mustJSON("POST", "/api/loops/waiting/pause", nil, nil)
	s.mustJSON("POST", "/api/loops/waiting/rehome", nil, nil)
	if view := s.loop("waiting"); !view.Rehoming || view.Runtime != "bare" {
		t.Fatalf("a paused loop asked to rehome reads rehoming=%v runtime=%q, want pending on the host", view.Rehoming, view.Runtime)
	}

	s.stop()
	s = startDockerServer(t, dataDir, "--allow-bare")
	if view := s.loop("waiting"); view.Rehoming || view.Runtime != "bare" {
		t.Fatalf("after a restart lost the request: rehoming=%v runtime=%q, want neither", view.Rehoming, view.Runtime)
	}

	s.mustJSON("POST", "/api/loops/waiting/rehome", nil, nil)
	if !s.loop("waiting").Rehoming {
		t.Fatal("asked again after the restart, the rehome is not pending")
	}
	s.mustJSON("POST", "/api/loops/waiting/resume", nil, nil)
	s.message("waiting", "resumed on the host")
	within(t, 60*time.Second, "the move lands and the pending state clears", func() bool {
		view := s.loop("waiting")
		return view.Runtime == "docker" && !view.Rehoming
	})
	assertRehomed(t, s, "waiting", loopID, "you were moved into your own docker workstation")
}

// setLoopColumns writes columns of a stopped hub's loop row directly.
func setLoopColumns(t *testing.T, dataDir, loopID, assignments string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE loops SET `+assignments+` WHERE id=?`, loopID); err != nil {
		t.Fatal(err)
	}
}

// slug is the name claude gives a project's directory.
func slug(dir string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, dir)
}
