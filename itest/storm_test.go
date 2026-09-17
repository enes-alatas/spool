//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// stormLimit mirrors internal/route's cap on deliveries per ordered loop pair
// per hour. The test states it rather than importing it: the number is a
// promise to the operator ("a relay cannot run away"), and a row that read
// the constant would keep passing if the constant changed by accident.
const stormLimit = 12

// TestStormGuardHaltsALoopRelay, ported from scripts/e2e/m4.sh (#3): two
// loops that answer each other by sending to the group will relay forever —
// each delivery wakes the other, whose turn sends again. The storm guard is
// the only thing that stops it, and this pins where it stops: twelve
// deliveries per direction, the rest dropped and recorded as events an
// operator can see.
func TestStormGuardHaltsALoopRelay(t *testing.T) {
	operator := user{ID: 9191, First: "Operator", Username: "operator"}
	// line 1 absorbs each loop's creation tick; line 2 is the relay, and it
	// repeats for every turn after
	alpha := workspaceWithScript(t, "!ctx 0\n"+`!send {"destination":"group","text":"@beta relay"}`+"\n")
	beta := workspaceWithScript(t, "!ctx 0\n"+`!send {"destination":"group","text":"@alpha relay"}`+"\n")
	srv, _ := startTelegramFleet(t, operator,
		map[string]any{"workspace_path": alpha},
		map[string]any{"workspace_path": beta})

	srv.mustJSON("POST", "/api/loops/alpha/message",
		map[string]any{"author": "tester", "text": "start the relay", "destination": "group"}, nil)

	if !srv.hasEvent("alpha", "storm_drop", 60*time.Second) {
		t.Fatalf("the relay was never halted; alpha sent %d times",
			len(srv.activityWith("@beta relay")))
	}

	// the relay starves itself once both directions are capped: a loop that
	// is no longer delivered to has nothing to answer
	deadline := time.Now().Add(30 * time.Second)
	last := -1
	for {
		now := len(srv.activityWith("@beta relay")) + len(srv.activityWith("@alpha relay"))
		if now == last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay never settled: %d loop messages and counting", now)
		}
		last = now
		time.Sleep(2 * time.Second)
	}

	// Counted where the delivery lands — the envelopes beta was actually
	// given — rather than in the sending message's delivered_to, which is
	// filled in from the intended targets before the guard runs and so
	// includes the ones it refused (#143).
	delivered := 0
	for _, e := range srv.eventsQuery("beta", "limit=500") {
		if e.Type == "envelope" && strings.Contains(e.Payload, "@beta relay") {
			delivered++
		}
	}
	if delivered != stormLimit {
		t.Fatalf("beta was given %d relay envelopes, want the cap of %d", delivered, stormLimit)
	}
	if sent := len(srv.activityWith("@beta relay")); sent <= delivered {
		t.Fatalf("alpha sent %d and beta got %d: the guard dropped nothing", sent, delivered)
	}
}
