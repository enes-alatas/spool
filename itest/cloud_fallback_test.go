//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A start whose environment is gone between the hub's list and the CLI's
// start makes the CLI pick another of the account's environments, saying
// so only in its debug log (run U on #1078). The hub reads the log, ends
// the CLI before it creates the session, and records the loop's setup
// check as failed at the environment, so the loop holds rather than
// making a stray session at every wake, until a check passes (#1232).
func TestAStartInAnotherEnvironmentHoldsTheLoop(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	setUpCloudEnvironment(t, hub, "env_01Another", cloudTokenOf("cirrus"))
	gone := filepath.Join(hub.fkState, "environments", cloudEnvironmentOf("cirrus"), "gone")
	if err := os.WriteFile(gone, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(startedVMs(t, hub))

	hub.message("cirrus", "words for a gone environment")
	if held := hub.waitHealth("cirrus", "failing"); len(held.Problems) != 1 || held.Problems[0].Symptom != store.SymptomCloudSetup {
		t.Fatalf("problems after a start in another environment = %+v, want one cloud_setup", held.Problems)
	}
	var setup cloudLoopSetup
	hub.mustJSON("GET", "/api/loops/cirrus/cloud/setup", nil, &setup)
	if setup.Check == nil || setup.Check.State != "failed" || setup.Check.Stage != "environment" ||
		!strings.Contains(setup.Check.Detail, "picked environment env_01Another") {
		t.Fatalf("the check after the start = %+v, want failed at environment, naming the pick", setup.Check)
	}
	hub.message("cirrus", "words while held")
	time.Sleep(2 * time.Second)
	if after := len(startedVMs(t, hub)); after != before {
		t.Errorf("starts in another environment made %d sessions, want none", after-before)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	passCloudCheck(t, hub, "cirrus")
	turn := hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "words while held") })
	if turn.IsError {
		t.Fatalf("the held message's turn once a check passed: %+v", turn)
	}
	for _, vm := range startedVMs(t, hub) {
		if environment, _ := os.ReadFile(filepath.Join(hub.fkState, "cloud", vm, "environment")); string(environment) != cloudEnvironmentOf("cirrus") {
			t.Errorf("session %s runs in %q, want every one in %s", vm, environment, cloudEnvironmentOf("cirrus"))
		}
	}
}
