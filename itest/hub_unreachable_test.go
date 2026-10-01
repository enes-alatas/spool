//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A loop whose claude cannot reach the hub runs its turns without the Spool
// tools: until #476 it said nothing to anyone, and the fleet showed it
// healthy. Now the CLI's init names the failure before the turn's first API
// call, so the loop is down for a named reason, and what it was told waits
// in the queue, unanswered, for a spawn that connects. The fix is outside
// Spool, so the loop finds it by retrying, and the alert clears on the
// first spawn whose Spool MCP server connects.
func TestALoopThatCannotReachTheHubIsDownAndItsWorkWaits(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	failed := filepath.Join(s.fkState, "mcp-failed")
	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.createLoop("aster", nil)

	s.waitState("aster", "workstation_down", 30*time.Second)
	v := s.loop("aster")
	if v.DownReason != "hub_unreachable" {
		t.Fatalf("down_reason = %q, want hub_unreachable", v.DownReason)
	}
	for _, want := range []string{"Spool MCP server failed", "loop listener"} {
		if !strings.Contains(v.WorkstationDetail, want) {
			t.Fatalf("workstation_detail = %q, want it to carry %q", v.WorkstationDetail, want)
		}
	}

	before := newestEventID(s.eventsQuery("aster", "limit=200"))
	s.message("aster", "are you there")
	retried := false
	for deadline := time.Now().Add(30 * time.Second); !retried && time.Now().Before(deadline); {
		for _, e := range s.eventsQuery("aster", "limit=200") {
			retried = retried || (e.Subtype == "retry_scheduled" && e.ID > before)
		}
		if !retried {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !retried {
		t.Fatal("a message to a loop that cannot reach the hub scheduled no retry; nothing would notice the hub coming back")
	}
	if v = s.loop("aster"); v.DownReason != "hub_unreachable" {
		t.Fatalf("down_reason = %q after a retry's spawn, want the alert held until a spawn connects", v.DownReason)
	}
	for _, tn := range s.turns("aster") {
		if !tn.IsError || tn.ResultText != "" {
			t.Fatalf("a turn ran without the hub's tools: %+v", tn)
		}
	}

	// Nothing new arrives from here on: only the retry can find the hub
	// back.
	if err := os.Remove(failed); err != nil {
		t.Fatal(err)
	}
	s.waitTurn("aster", 60*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "are you there")
	})
	if v = s.loop("aster"); v.DownReason != "" || v.State == "workstation_down" {
		t.Fatalf("after a spawn connected: state %q, down_reason %q, want the alert cleared", v.State, v.DownReason)
	}
	if !s.hasEvent("aster", "workstation_up", 5*time.Second) {
		t.Fatal("the alert cleared without a workstation_up event on the loop's timeline")
	}
}
