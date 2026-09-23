//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A loop is in the fleet channel or it is not (ADR-0032 item 2). One outside
// it has no group: a mention of it there is delivered to nobody, @all passes
// it by, and its own send to the group is refused in-turn. Moving it back in
// restores all three. None of it needs a surface.
func TestLoopOutsideTheFleetChannelHasNoGroup(t *testing.T) {
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	if !s.loop("cedar").InFleetChannel {
		t.Fatal("a new loop is outside the fleet channel; it should start in it")
	}
	var patched loopView
	s.mustJSON("PATCH", "/api/loops/cedar", map[string]any{"in_fleet_channel": false}, &patched)
	if patched.InFleetChannel || s.loop("cedar").InFleetChannel {
		t.Fatal("cedar is still in the fleet channel after the operator took it out")
	}

	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	const named = "@briar @cedar the build is red"
	if res := callSend(t, aster, map[string]any{"destination": "group", "text": named}); res.IsError {
		t.Fatalf("group send refused: %s", resultText(res))
	}
	const broadcast = "@all standup moved"
	if res := callSend(t, aster, map[string]any{"destination": "group", "text": broadcast}); res.IsError {
		t.Fatalf("broadcast refused: %s", resultText(res))
	}
	for _, text := range []string{named, broadcast} {
		stored := s.activityWith(text)
		if len(stored) != 1 {
			t.Fatalf("%q stored %d times, want 1", text, len(stored))
		}
		if got := stored[0].DeliveredTo; len(got) != 1 || got[0] != s.loop("briar").ID {
			t.Fatalf("%q delivered_to = %v, want briar alone: cedar is outside the fleet channel", text, got)
		}
	}
	// A mention that names only the loop outside addresses nobody known.
	wantSendError(t, callSend(t, aster, map[string]any{"destination": "group", "text": "@cedar are you there"}), "no_recipients")

	cedar := mcpSession(t, s, hubMCPToken(t, s, "cedar"))
	wantSendError(t, callSend(t, cedar, map[string]any{"destination": "group", "text": "@aster let me in"}), "no_such_destination")
	resp, body := s.do("POST", "/api/loops/cedar/message", map[string]any{"text": "@aster hello", "destination": "group"})
	if resp.StatusCode != 409 {
		t.Fatalf("a group post from the composer of a loop outside the fleet channel = %d %s, want 409", resp.StatusCode, body)
	}

	s.waitTurn("briar", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "standup moved")
	})
	time.Sleep(2 * time.Second) // let any wrong delivery land before looking
	for _, tn := range s.completed("cedar") {
		if tn.Trigger == "message" {
			t.Fatalf("a loop outside the fleet channel was woken by the group: %s", dump(tn))
		}
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "let me in") || strings.Contains(m.Text, "@aster hello") {
			t.Fatalf("a refused group post was stored: %s", dump(m))
		}
	}

	s.mustJSON("PATCH", "/api/loops/cedar", map[string]any{"in_fleet_channel": true}, &patched)
	if !patched.InFleetChannel {
		t.Fatal("cedar is still outside the fleet channel after the operator put it back")
	}
	if res := callSend(t, aster, map[string]any{"destination": "group", "text": "@cedar welcome back"}); res.IsError {
		t.Fatalf("group send to a loop back in the fleet channel refused: %s", resultText(res))
	}
	s.waitTurn("cedar", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "welcome back")
	})
	if res := callSend(t, cedar, map[string]any{"destination": "group", "text": "@aster glad to be here"}); res.IsError {
		t.Fatalf("a loop back in the fleet channel still cannot post to it: %s", resultText(res))
	}
}

// A human posting in the mirrored Telegram group reaches the loops in the
// fleet channel and not a loop outside it, even one whose bot sits in that
// very group: ingest is not delivery (ADR-0020 §2, ADR-0032 item 5).
func TestTelegramMentionOfALoopOutsideTheFleetChannelReachesNobody(t *testing.T) {
	operator := user{ID: 8484, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.mustJSON("PATCH", "/api/loops/beta", map[string]any{"in_fleet_channel": false}, nil)

	const text = "@alpha @beta_bot who owns the deploy?"
	tg.post(groupChatID, "supergroup", text, operator)
	srv.waitForMessage(text)
	time.Sleep(2 * time.Second)

	stored := srv.activityWith(text)
	if len(stored) != 1 {
		t.Fatalf("stored %d times, want 1", len(stored))
	}
	if got := stored[0].DeliveredTo; len(got) != 1 || got[0] != srv.loop("alpha").ID {
		t.Fatalf("delivered_to = %v, want alpha alone: beta is outside the fleet channel", got)
	}
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" && strings.Contains(tn.ResultText, "who owns the deploy") {
			t.Fatalf("a loop outside the fleet channel was woken from the Telegram group: %s", dump(tn))
		}
	}
}
