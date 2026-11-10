//go:build integration

package itest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// There is no fleet channel any more (ADR-0068): group names no
// destination, and channel:group no channel. A loop asking for either is
// refused in-turn with the destinations it has, nothing is stored, and the
// channel the upgrade made of the fleet channel is reached as any other.
func TestGroupIsNoDestinationAnyMore(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		hub.createLoop(name, nil)
	}
	aster := mcpSession(t, hub, hubMCPToken(t, hub, "aster"))
	for _, destination := range []string{"group", "channel:group"} {
		res := callSend(t, aster, map[string]any{"destination": destination, "text": "@briar via " + destination})
		wantSendError(t, res, "no_such_destination")
		if text := resultText(res); !strings.Contains(text, "channel:fleet") {
			t.Errorf("the refusal of %s lists no channel aster is in: %s", destination, text)
		}
		if got := hub.activityWith("via " + destination); len(got) != 0 {
			t.Fatalf("a refused send to %s was stored: %s", destination, dump(got))
		}
	}
	if resp, _ := hub.do("GET", "/api/group", nil); resp.StatusCode != 404 {
		t.Errorf("GET /api/group = %d, want 404: the fleet channel's own route went with it", resp.StatusCode)
	}

	if res := callSend(t, aster, map[string]any{"destination": "channel:fleet", "text": "@briar the build is red"}); res.IsError {
		t.Fatalf("a send to channel:fleet refused: %s", resultText(res))
	}
	hub.waitTurn("briar", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "the build is red")
	})
}

// A human posting in a bound Telegram group reaches the loops of the
// channel it carries and not a loop outside that channel, even one whose
// bot sits in that very group: its room there is unbound, and ingest is
// not delivery (ADR-0020, ADR-0068).
func TestTelegramMentionOfALoopOutsideTheChannelReachesNobody(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8484, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator, nil, map[string]any{"fleet": false})

	const text = "@alpha @beta_bot who owns the deploy?"
	tg.post(groupChatID, "supergroup", text, operator)
	srv.waitForMessage(text)
	time.Sleep(2 * time.Second)

	stored := srv.activityWith(text)
	if len(stored) != 1 {
		t.Fatalf("stored %d times, want 1", len(stored))
	}
	if got := stored[0].DeliveredTo; len(got) != 1 || got[0] != srv.loop("alpha").ID {
		t.Fatalf("delivered_to = %v, want alpha alone: beta is in no channel", got)
	}
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" && strings.Contains(tn.ResultText, "who owns the deploy") {
			t.Fatalf("a loop in no channel was woken from the Telegram group: %s", dump(tn))
		}
	}
}

// A new loop joins its team's home channel when the team has one, and no
// channel otherwise: there is no fleet-wide channel to default to, and no
// second loop makes one (ADR-0068). A fresh hub has no channel at all.
func TestANewLoopJoinsItsTeamsHomeOrNoChannel(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		hub.createLoop(name, map[string]any{"fleet": false})
	}
	var channels []channelJSON
	hub.mustJSON("GET", "/api/channels", nil, &channels)
	if len(channels) != 0 {
		t.Fatalf("a hub of two loops nobody put in a channel has channels %+v, want none", channels)
	}

	// a hub channel becomes the team's home in one step (ADR-0068)
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "ops"}, nil)
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red"}, nil)
	hub.mustJSON("PATCH", "/api/teams/red", map[string]any{"home_channel": "ops"}, nil)
	if got := hub.channel("ops"); got.Team != "red" {
		t.Fatalf("ops after naming it red's home = %+v, want it red's", got)
	}
	hub.createLoop("cedar", map[string]any{"fleet": false, "team": "red"})
	if got := hub.channel("ops").Loops; !slices.Equal(got, []string{"cedar"}) {
		t.Fatalf("the red team's home holds %v, want cedar, made in the team", got)
	}
	hub.createLoop("delta", map[string]any{"fleet": false})
	hub.mustJSON("GET", "/api/channels", nil, &channels)
	for _, channel := range channels {
		if slices.Contains(channel.Loops, "delta") {
			t.Fatalf("delta, made in no team, joined %s", channel.Name)
		}
	}
}

// A channel's endpoints are keyed by no loop (ADR-0032, ADR-0038).
// The operator's post wakes exactly the loops its text addresses in
// the channel — no loop is implied by where it was posted from — and one
// that addresses nobody is kept and wakes nobody. The timeline is the
// channel's whole conversation, loop posts included, and nothing private.
func TestAnOperatorPostWakesOnlyTheLoopsItAddresses(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		hub.createLoop(name, nil)
	}
	hub.mustJSON("DELETE", "/api/channels/fleet/loops/cedar", nil, nil)

	const addressed = "@aster @cedar the plan changed"
	const chatter = "morning, nobody in particular"
	for _, text := range []string{addressed, chatter} {
		if resp, body := hub.do("POST", "/api/channels/fleet/messages", map[string]any{"text": text}); resp.StatusCode != 202 {
			t.Fatalf("posting %q to the channel = %d %s, want 202", text, resp.StatusCode, body)
		}
	}
	if resp, _ := hub.do("POST", "/api/channels/fleet/messages", map[string]any{"text": "   "}); resp.StatusCode != 400 {
		t.Fatalf("an empty post to the channel = %d, want 400", resp.StatusCode)
	}
	hub.message("briar", "a private control room note")

	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "the plan changed")
	})
	aster := mcpSession(t, hub, hubMCPToken(t, hub, "aster"))
	const reply = "@briar picking it up"
	if res := callSend(t, aster, map[string]any{"destination": "channel:fleet", "text": reply}); res.IsError {
		t.Fatalf("aster's group send refused: %s", resultText(res))
	}
	time.Sleep(2 * time.Second) // let any wrong delivery land before looking

	var timeline []activityMessage
	hub.mustJSON("GET", "/api/channels/fleet/messages", nil, &timeline)
	byText := map[string]activityMessage{}
	for _, message := range timeline {
		if message.Conversation != "group" {
			t.Fatalf("the channel's timeline carries a %s message: %s", message.Conversation, dump(message))
		}
		byText[message.Text] = message
	}
	for _, text := range []string{addressed, chatter, reply} {
		if _, ok := byText[text]; !ok {
			t.Fatalf("%q is missing from the channel's timeline: %s", text, dump(timeline))
		}
	}
	if message := byText[addressed]; message.Origin != "web" || message.Author != "operator" ||
		!slices.Equal(message.DeliveredTo, []string{hub.loop("aster").ID}) {
		t.Fatalf("the operator's post = %s, want a web post by the operator delivered to aster alone: "+
			"cedar is outside it and briar was not named", dump(message))
	}
	if message := byText[chatter]; len(message.DeliveredTo) != 0 {
		t.Fatalf("a post addressing nobody was delivered to %v, want nobody", message.DeliveredTo)
	}
	for _, tn := range hub.completed("cedar") {
		if tn.Trigger == "message" {
			t.Fatalf("cedar, outside it, was woken by it: %s", dump(tn))
		}
	}
	for _, tn := range hub.completed("briar") {
		if strings.Contains(tn.ResultText, "the plan changed") || strings.Contains(tn.ResultText, "nobody in particular") {
			t.Fatalf("briar was woken by an operator post that did not address it: %s", dump(tn))
		}
	}
}

// A post to a channel's endpoint is the operator's, so it stays
// on the hub (ADR-0032) while the loop it names is delivered and a
// human's Telegram post comes inward to the same timeline.
func TestAChannelPostStaysOnTheHub(t *testing.T) {
	t.Parallel()
	operator := user{ID: 4747, First: "Operator", Username: "operator"}
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"channel:fleet","text":"@beta alpha heard the channel"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	const inward = "a human in the telegram group"
	tg.post(groupChatID, "supergroup", inward, operator)
	srv.waitForMessage(inward)
	srv.mustJSON("POST", "/api/channels/fleet/messages", map[string]any{"text": "@alpha the channel is the hub's"}, nil)

	// alpha's answer is the barrier, as in TestOperatorWordsStayOnTheHub:
	// the mirror consumes in order, so an outward operator post would have
	// been sent before it.
	tg.waitSentFrom(t, groupChatID, "alpha", "alpha heard the channel")
	if sent, ok := tg.sentAnywhere("the channel is the hub's"); ok {
		t.Fatalf("the operator's channel post reached telegram (chat %d, bot %q): %q", sent.ChatID, sent.Token, sent.Text)
	}

	var timeline []activityMessage
	srv.mustJSON("GET", "/api/channels/fleet/messages", nil, &timeline)
	seen := map[string]bool{}
	for _, message := range timeline {
		seen[message.Text] = true
	}
	if !seen[inward] || !seen["@alpha the channel is the hub's"] {
		t.Fatalf("the channel's timeline lacks the Telegram post or the operator's: %s", dump(timeline))
	}
}

// The operator can reply to a message in a channel, as a human can
// natively in a mirrored room: the reply addresses the author of what it
// answers, so a loop is woken with no mention (ADR-0025). The target must
// be a message of the channel itself. One from a private conversation would
// add its author to the channel's recipients, and one that does not exist
// means the page is stale, so both are refused and nothing is stored.
func TestOperatorReplyInAChannelAddressesItsAuthor(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		hub.createLoop(name, nil)
	}
	aster := mcpSession(t, hub, hubMCPToken(t, hub, "aster"))
	const status = "@briar migration is halfway"
	if res := callSend(t, aster, map[string]any{"destination": "channel:fleet", "text": status}); res.IsError {
		t.Fatalf("aster's group send refused: %s", resultText(res))
	}
	posted := hub.activityWith(status)
	if len(posted) != 1 {
		t.Fatalf("aster's post stored %d times, want 1", len(posted))
	}
	hub.message("briar", "a private control room note")
	private := hub.activityWith("a private control room note")
	if len(private) != 1 {
		t.Fatalf("briar's control room message stored %d times, want 1", len(private))
	}

	const reply = "thanks, keep going"
	if resp, body := hub.do("POST", "/api/channels/fleet/messages", map[string]any{"text": reply, "reply_to_id": posted[0].ID}); resp.StatusCode != 202 {
		t.Fatalf("replying to aster's post = %d %s, want 202", resp.StatusCode, body)
	}
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, reply)
	})
	stored := hub.activityWith(reply)
	if len(stored) != 1 || stored[0].ReplyToID != posted[0].ID ||
		!slices.Equal(stored[0].DeliveredTo, []string{hub.loop("aster").ID}) {
		t.Fatalf("the reply = %s, want one message replying to %d and delivered to aster alone: "+
			"a reply addresses its target's author and inherits none of its recipients",
			dump(stored), posted[0].ID)
	}

	for _, refusal := range []struct {
		name, text string
		target     int64
		code       string
	}{
		{"a message in a private conversation", "crossing over", private[0].ID, "cross_conversation_reply_to"},
		{"a message that does not exist", "answering nothing", 1 << 40, "unknown_reply_to"},
	} {
		resp, body := hub.do("POST", "/api/channels/fleet/messages", map[string]any{"text": refusal.text, "reply_to_id": refusal.target})
		if resp.StatusCode != 400 || !strings.Contains(string(body), refusal.code) {
			t.Fatalf("replying to %s = %d %s, want 400 %s", refusal.name, resp.StatusCode, body, refusal.code)
		}
		// The target's conversation is not the replier's to learn: a member
		// walking message ids would map the private ones (#719).
		if strings.Contains(string(body), "control_room") {
			t.Fatalf("the refusal of a reply to %s names where it is: %s", refusal.name, body)
		}
		if got := hub.activityWith(refusal.text); len(got) != 0 {
			t.Fatalf("a refused reply to %s was stored: %s", refusal.name, dump(got))
		}
	}
}
