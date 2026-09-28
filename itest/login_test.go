//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A login the API refuses fails every turn the same way, and until #405 each
// one was an ordinary errored turn: the loop looked alive, did nothing, and
// dropped what it was told. Now the loop is down for a named reason, the one
// fix the operator has is in the alert, and what it was told waits for the
// login to come back. Logging in again happens outside Spool, so the loop
// finds out by retrying, and the alert clears on the first turn that runs.
func TestARejectedLoginIsNamedAndTheLoopsWorkWaitsForIt(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	expired := filepath.Join(s.fkState, "login-expired")
	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.createLoop("aster", nil)

	s.waitState("aster", "workstation_down", 30*time.Second)
	v := s.loop("aster")
	if v.DownReason != "unauthenticated" {
		t.Fatalf("down_reason = %q, want unauthenticated", v.DownReason)
	}
	for _, want := range []string{"OAuth session expired", "log in again with claude on the host"} {
		if !strings.Contains(v.WorkstationDetail, want) {
			t.Fatalf("workstation_detail = %q, want it to carry %q", v.WorkstationDetail, want)
		}
	}

	// What the loop is told while its login is refused is not handed to a
	// turn that never reaches the model: it is refused too, and kept.
	sent := time.Now().UnixMilli()
	before := newestEventID(s.eventsQuery("aster", "limit=200"))
	s.message("aster", "are you there")
	s.waitTurn("aster", 60*time.Second, func(tn turn) bool {
		return tn.IsError && tn.StartedAt >= sent
	})
	// The creation tick's refusal scheduled a retry already; this one has
	// to be the message's.
	retried := false
	for deadline := time.Now().Add(5 * time.Second); !retried && time.Now().Before(deadline); {
		for _, e := range s.eventsQuery("aster", "limit=200") {
			retried = retried || (e.Subtype == "retry_scheduled" && e.ID > before)
		}
		if !retried {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !retried {
		t.Fatal("a refused message scheduled no retry; nothing would notice the login coming back")
	}
	if v = s.loop("aster"); v.DownReason != "unauthenticated" {
		t.Fatalf("down_reason = %q after a retry's spawn, want the alert held until a turn runs", v.DownReason)
	}

	// Nothing new arrives from here on: only the retry can find the login
	// back.
	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	s.waitTurn("aster", 60*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "are you there")
	})
	if v = s.loop("aster"); v.DownReason != "" || v.State == "workstation_down" {
		t.Fatalf("after a turn ran: state %q, down_reason %q, want the alert cleared", v.State, v.DownReason)
	}
	if !s.hasEvent("aster", "workstation_up", 5*time.Second) {
		t.Fatal("the alert cleared without a workstation_up event on the loop's timeline")
	}
}

// newestEventID is the highest event id in events, 0 when there are none.
// Ids only grow, so what comes after it is what happened since.
func newestEventID(events []spoolEvent) int64 {
	var newest int64
	for _, e := range events {
		newest = max(newest, e.ID)
	}
	return newest
}
