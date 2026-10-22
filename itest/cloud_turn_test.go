//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cloudTurnJSON is a cloud loop's view of its turn that may be running.
type cloudTurnJSON struct {
	CloudSessionURL string `json:"cloud_session_url"`
	CloudTurn       *struct {
		Since      int64  `json:"since"`
		Until      int64  `json:"until"`
		SessionURL string `json:"session_url"`
		CutOff     bool   `json:"cut_off"`
	} `json:"cloud_turn"`
}

// A cloud loop's view says a turn is running from the hub's send until its
// Stop post, and links the turn's session. A stop revokes the token, so
// the turn the VM runs on is cut off: its Stop post never reaches the hub,
// and the view keeps it until its quiet bound, two hours from the send. A
// new session after the stop leaves it there, linked to its own session
// (ADR-0059).
func TestACloudLoopSaysATurnMayStillBeRunning(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	view := func() cloudTurnJSON {
		t.Helper()
		var viewed cloudTurnJSON
		hub.mustJSON("GET", "/api/loops/cirrus", nil, &viewed)
		return viewed
	}

	hub.message("cirrus", "first words")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	if viewed := view(); viewed.CloudTurn != nil {
		t.Fatalf("a turn whose Stop post came still shows: %+v", viewed.CloudTurn)
	}
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("the loop made %d sessions, want 1", len(vms))
	}
	first := "https://claude.ai/code/" + vms[0]

	held := filepath.Join(hub.fkState, "cloud", vms[0], "turn-held")
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(held) })
	sent := time.Now().UnixMilli()
	hub.waitState("cirrus", "asleep", 20*time.Second)
	hub.message("cirrus", "a long turn")
	var running cloudTurnJSON
	within(t, 20*time.Second, "the running turn on the view", func() bool {
		running = view()
		return running.CloudTurn != nil
	})
	if turn := running.CloudTurn; turn.CutOff || turn.SessionURL != first || turn.Since < sent ||
		turn.Until != turn.Since+(2*time.Hour).Milliseconds() {
		t.Fatalf("the running turn = %+v, want one in %s since the send, bound two hours on, not cut off", turn, first)
	}

	hub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	var stopped cloudTurnJSON
	within(t, 10*time.Second, "the stop cutting the turn off", func() bool {
		stopped = view()
		return stopped.CloudTurn != nil && stopped.CloudTurn.CutOff
	})
	if turn := stopped.CloudTurn; turn.Since != running.CloudTurn.Since || turn.SessionURL != first {
		t.Errorf("the cut-off turn = %+v, want the running one", turn)
	}

	// the VM's turn ends, and its Stop post, on a revoked token, is refused
	if err := os.Remove(held); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if viewed := view(); viewed.CloudTurn == nil || !viewed.CloudTurn.CutOff {
			t.Fatalf("the cut-off turn went from the view: %+v", viewed.CloudTurn)
		}
	}

	hub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)
	hub.message("cirrus", "after the stop")
	hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "after the stop") })
	after := view()
	if after.CloudSessionURL == first || after.CloudTurn == nil || after.CloudTurn.SessionURL != first || !after.CloudTurn.CutOff {
		t.Errorf("after a new session: link %q, turn %+v; want the new session linked and the cut-off turn kept in %s",
			after.CloudSessionURL, after.CloudTurn, first)
	}
}
