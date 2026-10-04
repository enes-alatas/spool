//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type pillarJSON struct {
	Done     bool   `json:"done"`
	Reason   string `json:"reason"`
	Checking bool   `json:"checking"`
}

type onboardingJSON struct {
	Completed bool       `json:"completed"`
	Harness   pillarJSON `json:"harness"`
	Surface   pillarJSON `json:"surface"`
	Loops     pillarJSON `json:"loops"`
}

func (s *server) onboarding() onboardingJSON {
	s.t.Helper()
	var view onboardingJSON
	s.mustJSON("GET", "/api/onboarding", nil, &view)
	return view
}

// waitOnboarding polls the readiness read until ok holds, as the first-run
// page does.
func (s *server) waitOnboarding(within time.Duration, ok func(onboardingJSON) bool) onboardingJSON {
	s.t.Helper()
	var view onboardingJSON
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if view = s.onboarding(); ok(view) {
			return view
		}
	}
	s.t.Fatalf("onboarding never got there; last read %+v", view)
	return view
}

// The first-run page's read (#580): each pillar goes done as the operator
// gets there, the hub remembers once all three were, and a pillar that
// later reads not done says so without bringing the page back; deleting the
// last loop does.
func TestOnboardingPillars(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6868, First: "Operator", Username: "operator"}
	// line 1 answers the creation tick; line 2 the operator's DM
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"owner_dm","text":"hello from alpha"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// The creation ticks wake both loops, so on a bare hub harness and
	// loops are done before any message is exchanged.
	view := srv.waitOnboarding(30*time.Second, func(v onboardingJSON) bool { return v.Loops.Done })
	if view.Completed || view.Surface.Done {
		t.Fatalf("before the loop sent anything: %+v, want the surface not done and not completed", view)
	}
	if !view.Harness.Done || view.Harness.Reason != "a turn authenticated" {
		t.Errorf("harness after a turn = %+v, want done by a turn", view.Harness)
	}
	if view.Surface.Reason != "no chat surface has carried a message yet" {
		t.Errorf("surface reason before any message = %q", view.Surface.Reason)
	}

	// The operator writes, and the turn it wakes writes back.
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")
	tg.waitSent(t, operator.ID, "hello from alpha")
	view = srv.waitOnboarding(10*time.Second, func(v onboardingJSON) bool { return v.Completed })
	if !view.Surface.Done || !view.Harness.Done || !view.Loops.Done {
		t.Fatalf("completed with a pillar not done: %+v", view)
	}

	// A refused login turns the harness card red; the page stays aside.
	if err := os.MkdirAll(srv.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.fkState, "login-expired"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	srv.message("beta", "are you there")
	srv.waitState("beta", "workstation_down", 30*time.Second)
	view = srv.onboarding()
	if view.Harness.Done || !strings.Contains(view.Harness.Reason, "refused on loop beta") || !view.Completed {
		t.Fatalf("after beta's login was refused: %+v, want harness not done, naming beta, and still completed", view)
	}

	// Deleting one loop leaves the fleet onboarded; deleting the last
	// brings the page back.
	srv.mustJSON("DELETE", "/api/loops/beta", nil, nil)
	if view = srv.onboarding(); !view.Completed {
		t.Fatalf("after deleting beta, with alpha left: %+v, want still completed", view)
	}
	srv.mustJSON("DELETE", "/api/loops/alpha", nil, nil)
	view = srv.onboarding()
	if view.Completed || view.Loops.Done || view.Loops.Reason != "no loops" || view.Surface.Done {
		t.Fatalf("after deleting the last loop: %+v, want not completed, loops reading no loops, "+
			"and the surface not done on the deleted loops' traffic", view)
	}
}

// On a docker hub, saving a setup-token checks it (ADR-0044): one run of
// the workstation image's claude under the token, behind the egress wall,
// whose answer makes the harness pillar done. Removing the token forgets
// the check, and asking for one with no token to check is refused.
func TestOnboardingChecksASavedSetupToken(t *testing.T) {
	t.Parallel()
	s := startDockerServer(t, t.TempDir()) // saves a token as it starts
	got := s.waitOnboarding(90*time.Second, func(v onboardingJSON) bool { return !v.Harness.Checking })
	if !got.Harness.Done || got.Harness.Reason != "the login check authenticated" {
		t.Fatalf("harness after the saved token was checked = %+v", got.Harness)
	}

	s.mustJSON("PUT", "/api/settings", map[string]any{"claude_oauth_token": ""}, nil)
	if got := s.onboarding().Harness; got.Done || got.Reason != "no setup-token saved in Settings" {
		t.Errorf("harness with no token = %+v", got)
	}
	s.wantRefusal("POST", "/api/onboarding/harness-check", nil, 409, "no_setup_token")
	view := s.onboarding()
	if view.Completed || view.Surface.Done || view.Loops.Reason != "no loops" {
		t.Errorf("a fresh hub = %+v, want nothing done", view)
	}
}

// On a bare hub the check runs the host's claude under the operator's own
// login, only when asked (ADR-0044). It reports a login the API accepts, and
// a refused one with the CLI's sentence; a turn that authenticates after a
// refused check is newer evidence and wins.
func TestOnboardingChecksTheHostLogin(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	if got := s.onboarding().Harness; got.Done || got.Reason != "the host's claude login is not checked yet" {
		t.Fatalf("harness on a fresh bare hub = %+v", got)
	}

	// a check the page can wait on: checking until it answers
	slow := filepath.Join(s.fkState, "login-slow")
	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slow, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var started onboardingJSON
	s.mustJSON("POST", "/api/onboarding/harness-check", nil, &started)
	if got := started.Harness; got.Done || !got.Checking || got.Reason != "the host's claude login is being checked" {
		t.Fatalf("harness as the check starts = %+v, want checking", got)
	}
	got := s.waitOnboarding(30*time.Second, func(v onboardingJSON) bool { return !v.Harness.Checking })
	if !got.Harness.Done || got.Harness.Reason != "the login check authenticated" {
		t.Fatalf("harness after an accepted check = %+v", got.Harness)
	}
	if err := os.Remove(slow); err != nil {
		t.Fatal(err)
	}

	expired := filepath.Join(s.fkState, "login-expired")
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.mustJSON("POST", "/api/onboarding/harness-check", nil, nil)
	got = s.waitOnboarding(30*time.Second, func(v onboardingJSON) bool { return !v.Harness.Done && strings.Contains(v.Harness.Reason, "refused") })
	if want := "the login check was refused: Failed to authenticate: OAuth session expired"; !strings.HasPrefix(got.Harness.Reason, want) {
		t.Fatalf("harness after a refused check = %+v, want the CLI's sentence", got.Harness)
	}

	// logging in again, then a loop's turn authenticating
	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	s.createLoop("aster", nil)
	got = s.waitOnboarding(30*time.Second, func(v onboardingJSON) bool { return v.Harness.Done })
	if got.Harness.Reason != "a turn authenticated" {
		t.Fatalf("harness after a turn authenticated = %+v", got.Harness)
	}
}
