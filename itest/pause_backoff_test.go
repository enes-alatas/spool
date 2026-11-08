//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A pause that ends a turn is the operator's stop, not a crash (#1302): it
// schedules no retry, and the resume wakes the loop on the work the turn
// was handed as soon as it is let go, not after a backoff. Only a cloud
// loop's pause ends the turn in flight (a bare or docker pause lets it
// finish), so the pause runs on one; the branch it takes is the one every
// operator-ended stop takes. A turn whose process crashes on its own
// still backs off.
func TestAPauseMidTurnSchedulesNoRetry(t *testing.T) {
	t.Parallel()
	cloudHub := startCloudHub(t, "cirrus")
	cloudHub.message("cirrus", "first words")
	cloudHub.waitTurn("cirrus", 30*time.Second, func(turn) bool { return true })
	vms := cloudVMs(t, cloudHub)
	if len(vms) != 1 {
		t.Fatalf("the loop made %d sessions, want 1", len(vms))
	}
	held := filepath.Join(cloudHub.fkState, "cloud", vms[0], "turn-held")
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cloudHub.waitState("cirrus", "asleep", 20*time.Second)
	cloudHub.message("cirrus", "a long turn")
	cloudHub.waitState("cirrus", "busy", 20*time.Second)
	cloudHub.mustJSON("POST", "/api/loops/cirrus/pause", nil, nil)
	// the turn's process ends, as a crash or as a resume that never loaded
	within(t, 20*time.Second, "the pause ending the turn", func() bool {
		for _, event := range cloudHub.eventsQuery("cirrus", "limit=500") {
			if event.Subtype == "crash" || event.Subtype == "resume_failed" {
				return true
			}
		}
		return false
	})
	if err := os.Remove(held); err != nil {
		t.Fatal(err)
	}
	resumed := time.Now()
	cloudHub.mustJSON("POST", "/api/loops/cirrus/resume", nil, nil)
	// the turn the pause ended is said again on the resume, at once
	again := cloudHub.waitTurn("cirrus", 30*time.Second, func(tn turn) bool { return tn.StartedAt >= resumed.UnixMilli() })
	if waited := time.Duration(again.StartedAt-resumed.UnixMilli()) * time.Millisecond; waited > 5*time.Second {
		t.Errorf("the resumed loop woke %v after the resume, want at once", waited)
	}
	for _, event := range cloudHub.eventsQuery("cirrus", "limit=500") {
		if event.Subtype == "retry_scheduled" || event.Subtype == "resume_failed" {
			t.Errorf("a pause mid-turn recorded %s: %s", event.Subtype, event.Payload)
		}
	}

	bareHub := startServer(t, t.TempDir())
	bareHub.createLoop("crashed", map[string]any{"workspace_path": workspaceWithScript(t, "hello\n!crash\n")})
	bareHub.waitTurn("crashed", firstTurnWait, func(turn) bool { return true })
	bareHub.waitState("crashed", "asleep", 30*time.Second)
	bareHub.message("crashed", "this one dies")
	if waits := waitForRetries(t, bareHub, "crashed", 1, 30*time.Second); waits[0] != 10000 {
		t.Errorf("the crash's retry waits %dms, want the ladder's first rung, 10000", waits[0])
	}
}
