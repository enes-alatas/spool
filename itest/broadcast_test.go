//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// @all is deliberate group delivery to the eligible loops of the fleet
// channel (#74, ADR-0025, ADR-0032): the sender never wakes itself, overlap
// with mentions and reply addressing costs one delivery, paused loops and
// loops outside the fleet channel stay out, and private text containing @all
// is still private text.

// A human's @all in the group reaches every eligible loop once — including
// one with no surface, since the fleet channel is the hub's and not
// Telegram's — and skips the loop the operator paused and the one taken out
// of the fleet channel.
func TestHumanBroadcastReachesEligibleLoopsOnce(t *testing.T) {
	operator := user{ID: 8181, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.createLoop("gamma", nil)
	srv.createLoop("delta", nil)
	srv.mustJSON("POST", "/api/loops/beta/pause", nil, nil)
	srv.mustJSON("PATCH", "/api/loops/delta", map[string]any{"in_fleet_channel": false}, nil)

	const text = "@all standup in five"
	tg.post(groupChatID, "supergroup", text, operator)
	srv.waitForMessage(text)
	time.Sleep(2 * time.Second) // let any extra delivery land before counting

	stored := srv.activityWith(text)
	if len(stored) != 1 {
		t.Fatalf("broadcast stored %d times, want 1", len(stored))
	}
	delivered := map[string]bool{}
	for _, id := range stored[0].DeliveredTo {
		if delivered[id] {
			t.Fatalf("delivered_to has a duplicate: %v", stored[0].DeliveredTo)
		}
		delivered[id] = true
	}
	if got := len(delivered); got != 2 || !delivered[srv.loop("alpha").ID] || !delivered[srv.loop("gamma").ID] {
		t.Fatalf("delivered_to = %v, want alpha and gamma: beta is paused and delta is outside the fleet channel",
			stored[0].DeliveredTo)
	}
	for _, name := range []string{"beta", "delta"} {
		for _, tn := range srv.completed(name) {
			if tn.Trigger == "message" && strings.Contains(tn.ResultText, "standup in five") {
				t.Fatalf("an ineligible loop (%s) was woken by the broadcast", name)
			}
		}
	}
}

// A loop's own @all reaches its peers and never itself, and a peer named in
// the same text is delivered to once, not twice.
func TestLoopBroadcastExcludesItselfAndDeduplicates(t *testing.T) {
	operator := user{ID: 8282, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@all @beta the deploy is frozen"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	srv.message("alpha", "announce it")
	tg.waitSent(t, groupChatID, "the deploy is frozen")

	stored := srv.activityWith("@all @beta the deploy is frozen")
	if len(stored) != 1 {
		t.Fatalf("broadcast stored %d times, want 1", len(stored))
	}
	if got := stored[0].DeliveredTo; len(got) != 1 || got[0] != srv.loop("beta").ID {
		t.Fatalf("delivered_to = %v, want beta once — the sender excluded, the overlap deduplicated", got)
	}
	time.Sleep(2 * time.Second)
	for _, tn := range srv.completed("alpha") {
		if tn.Trigger == "message" && strings.Contains(tn.ResultText, "the deploy is frozen") {
			t.Fatalf("the sender woke itself through its own @all: %s", dump(tn))
		}
	}
}

// @all inside a DM is literal text: private conversations never expand
// mentions into recipients.
func TestBroadcastInADMStaysPrivate(t *testing.T) {
	operator := user{ID: 8383, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	const text = "@all can you all hear me?"
	tg.dm("alpha", operator, text)
	srv.waitForMessage(text)
	time.Sleep(2 * time.Second)

	stored := srv.activityWith(text)
	if len(stored) != 1 {
		t.Fatalf("DM stored %d times, want 1", len(stored))
	}
	if got := stored[0].DeliveredTo; len(got) != 1 || got[0] != srv.loop("alpha").ID {
		t.Fatalf("delivered_to = %v, want the DM's own loop alone", got)
	}
	if stored[0].Conversation != "owner_dm" {
		t.Fatalf("conversation = %q, want owner_dm", stored[0].Conversation)
	}
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" {
			t.Fatalf("a private @all woke another loop: %s", dump(tn))
		}
	}
}

// "all" cannot be a loop name: the token would be unmentionable.
func TestAllIsAReservedLoopName(t *testing.T) {
	s := startServer(t, t.TempDir())
	resp, body := s.do("POST", "/api/loops", map[string]any{
		"name": "all", "mission": "should never exist",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("creating a loop named all = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "reserved") {
		t.Fatalf("refusal does not say why: %s", body)
	}
}

// A pair the storm guard has already limited stays limited inside a
// broadcast: @all is deliberate addressing, not a way around the per-pair
// hourly cap. The loops it has not exhausted still receive it.
func TestBroadcastDoesNotBypassTheStormGuard(t *testing.T) {
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	// exhaust aster→briar: the guard allows 12 deliveries an hour. The
	// per-turn send cap is 10, so a turn boundary in the middle reopens
	// aster's budget without touching the guard.
	for i := 0; i < stormLimitPerHour; i++ {
		if i == 10 {
			s.message("aster", "a turn of its own")
			s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
				return strings.Contains(tn.ResultText, "a turn of its own")
			})
		}
		res := callSend(t, sess, map[string]any{
			"destination": "group", "text": "@briar filling the quota",
		})
		if res.IsError {
			t.Fatalf("send %d refused: %s", i+1, resultText(res))
		}
	}

	res := callSend(t, sess, map[string]any{
		"destination": "group", "text": "@all one past the limit",
	})
	if res.IsError {
		t.Fatalf("broadcast refused: %s", resultText(res))
	}

	// cedar is untouched by the guard and must receive it
	s.waitTurn("cedar", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "one past the limit")
	})
	if !s.hasEvent("aster", "storm_drop", 10*time.Second) {
		t.Fatal("the exhausted pair was not recorded as a storm drop")
	}
	for _, tn := range s.completed("briar") {
		if strings.Contains(tn.ResultText, "one past the limit") {
			t.Fatalf("the broadcast bypassed the storm guard: %s", dump(tn))
		}
	}
}

// stormLimitPerHour mirrors internal/route's per-pair cap; the test drives
// the guard through the public surface, so it has to know the number.
const stormLimitPerHour = 12
