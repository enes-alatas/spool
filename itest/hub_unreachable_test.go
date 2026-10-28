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
		// the turn is held, saying why, rather than run (#1096)
		if !tn.IsError || !strings.Contains(tn.ResultText, "Spool MCP server") {
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

// A hold kills the CLI at init, before the turn's first API call, and the
// CLI writes a session only after it, so the session that spawn began is
// never written. The next spawn begins another rather than resuming it,
// which would fail as an unknown session and rotate the loop for a loss
// that never happened (#1163). The turn is held two seconds past init, so
// the kill always lands before the fake writes the session, as it does on
// the real CLI.
func TestAHeldFirstSpawnBeginsAnotherSessionRatherThanResumingIt(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	if err := os.MkdirAll(hub.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"mcp-failed", "login-slow"} {
		if err := os.WriteFile(filepath.Join(hub.fkState, marker), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hub.createLoop("aster", nil)
	hub.waitState("aster", "workstation_down", 30*time.Second)

	var spawns, exits []spoolEvent
	for deadline := time.Now().Add(40 * time.Second); len(exits) < 2 && time.Now().Before(deadline); {
		spawns, exits = nil, nil
		for _, event := range hub.eventsQuery("aster", "limit=200") {
			switch event.Subtype {
			case "proc_spawn":
				spawns = append(spawns, event)
			case "proc_exit":
				exits = append(exits, event)
			case "session_lost", "resume_failed", "session_unusable":
				t.Fatalf("the held session was taken for lost: %s %s", event.Subtype, event.Payload)
			}
		}
		if len(exits) < 2 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if len(exits) < 2 {
		t.Fatalf("saw %d spawns and %d exits, want the retry's spawn to have ended too", len(spawns), len(exits))
	}
	for _, spawn := range spawns {
		if !strings.Contains(spawn.Payload, `"resume":false`) {
			t.Fatalf("a spawn resumed the session its first process never wrote: %s", spawn.Payload)
		}
	}
	for _, tn := range hub.turns("aster") {
		if !tn.IsError || !strings.Contains(tn.ResultText, "Spool MCP server") {
			t.Fatalf("a turn did not say it was held: %+v", tn)
		}
	}
}
