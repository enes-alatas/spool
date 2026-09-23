//go:build integration

package itest

import (
	"slices"
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

// The fleet channel has endpoints of its own, keyed by no loop (ADR-0032
// item 1). The operator's post wakes exactly the loops its text addresses in
// the channel — no loop is implied by where it was posted from — and one
// that addresses nobody is kept and wakes nobody. The timeline is the
// channel's whole conversation, loop posts included, and nothing private.
func TestOperatorPostsToTheFleetChannel(t *testing.T) {
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("PATCH", "/api/loops/cedar", map[string]any{"in_fleet_channel": false}, nil)

	const addressed = "@aster @cedar the plan changed"
	const chatter = "morning, nobody in particular"
	for _, text := range []string{addressed, chatter} {
		if resp, body := s.do("POST", "/api/group", map[string]any{"text": text}); resp.StatusCode != 202 {
			t.Fatalf("posting %q to the fleet channel = %d %s, want 202", text, resp.StatusCode, body)
		}
	}
	if resp, _ := s.do("POST", "/api/group", map[string]any{"text": "   "}); resp.StatusCode != 400 {
		t.Fatalf("an empty post to the fleet channel = %d, want 400", resp.StatusCode)
	}
	s.message("briar", "a private control room note")

	s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "the plan changed")
	})
	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	const reply = "@briar picking it up"
	if res := callSend(t, aster, map[string]any{"destination": "group", "text": reply}); res.IsError {
		t.Fatalf("aster's group send refused: %s", resultText(res))
	}
	time.Sleep(2 * time.Second) // let any wrong delivery land before looking

	var timeline []activityMessage
	s.mustJSON("GET", "/api/group", nil, &timeline)
	byText := map[string]activityMessage{}
	for _, m := range timeline {
		if m.Conversation != "group" {
			t.Fatalf("the fleet channel's timeline carries a %s message: %s", m.Conversation, dump(m))
		}
		byText[m.Text] = m
	}
	for _, text := range []string{addressed, chatter, reply} {
		if _, ok := byText[text]; !ok {
			t.Fatalf("%q is missing from the fleet channel's timeline: %s", text, dump(timeline))
		}
	}
	if m := byText[addressed]; m.Origin != "web" || m.Author != "operator" ||
		!slices.Equal(m.DeliveredTo, []string{s.loop("aster").ID}) {
		t.Fatalf("the operator's post = %s, want a web post by the operator delivered to aster alone: "+
			"cedar is outside the fleet channel and briar was not named", dump(m))
	}
	if m := byText[chatter]; len(m.DeliveredTo) != 0 {
		t.Fatalf("a post addressing nobody was delivered to %v, want nobody", m.DeliveredTo)
	}
	for _, tn := range s.completed("cedar") {
		if tn.Trigger == "message" {
			t.Fatalf("cedar, outside the fleet channel, was woken by it: %s", dump(tn))
		}
	}
	for _, tn := range s.completed("briar") {
		if strings.Contains(tn.ResultText, "the plan changed") || strings.Contains(tn.ResultText, "nobody in particular") {
			t.Fatalf("briar was woken by an operator post that did not address it: %s", dump(tn))
		}
	}
}

// A post to the fleet channel's own endpoint is the operator's, so it stays
// on the hub (ADR-0032 item 4) while the loop it names is delivered and a
// human's Telegram post comes inward to the same timeline.
func TestFleetChannelPostStaysOnTheHub(t *testing.T) {
	operator := user{ID: 4747, First: "Operator", Username: "operator"}
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta alpha heard the channel"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	const inward = "a human in the telegram group"
	tg.post(groupChatID, "supergroup", inward, operator)
	srv.waitForMessage(inward)
	srv.mustJSON("POST", "/api/group", map[string]any{"text": "@alpha the channel is the hub's"}, nil)

	// alpha's answer is the barrier, as in TestOperatorWordsStayOnTheHub:
	// the mirror consumes in order, so an outward operator post would have
	// been sent before it.
	tg.waitSentFrom(t, groupChatID, "alpha", "alpha heard the channel")
	if m, ok := tg.sentAnywhere("the channel is the hub's"); ok {
		t.Fatalf("the operator's fleet channel post reached telegram (chat %d, bot %q): %q", m.ChatID, m.Token, m.Text)
	}

	var timeline []activityMessage
	srv.mustJSON("GET", "/api/group", nil, &timeline)
	seen := map[string]bool{}
	for _, m := range timeline {
		seen[m.Text] = true
	}
	if !seen[inward] || !seen["@alpha the channel is the hub's"] {
		t.Fatalf("the fleet channel's timeline lacks the Telegram post or the operator's: %s", dump(timeline))
	}
}
