//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A cloud turn the API fails is over as soon as it ends (#1280). The CLI
// runs StopFailure instead of Stop, and the setup script's hook posts it as
// it posts Stop, so the hub ends the turn as an error with the CLI's
// sentence, not after two quiet hours. The loop then sleeps, and its next
// wake goes into the same session.
func TestACloudTurnTheAPIFailsEndsAsAnError(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "nimbus")

	hub.message("nimbus", "a turn the API fails\n!api-error rate_limit")
	failed := hub.waitTurn("nimbus", 30*time.Second, func(done turn) bool { return done.IsError })
	if failed.ResultText != "API Error: rate_limit" {
		t.Fatalf("the failed turn: %+v, want an error with the CLI's sentence", failed)
	}
	if strings.Contains(hub.log(), "cloud hook refused") {
		t.Errorf("the hub refused a hook post:\n%s", hub.log())
	}

	hub.waitState("nimbus", "asleep", 20*time.Second)
	hub.message("nimbus", "words after the failure")
	next := hub.waitTurn("nimbus", 30*time.Second, func(done turn) bool { return strings.Contains(done.ResultText, "words after the failure") })
	if next.IsError || next.SessionID != failed.SessionID {
		t.Fatalf("the next wake's turn: %+v, want a reply in session %s", next, failed.SessionID)
	}
	if vms := cloudVMs(t, hub); len(vms) != 1 {
		t.Errorf("the loop made %d sessions, want the failed turn's one", len(vms))
	}
}
