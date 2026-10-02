//go:build integration

package itest

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// pushReaction sends a reaction_added or reaction_removed event down app's
// connection: user's emoji, by Slack's name for it, on the message Slack
// knows as ts in channel.
func (slack *fakeSlack) pushReaction(t *testing.T, app, user, name, channel, ts string, removed bool) {
	t.Helper()
	kind := "reaction_added"
	if removed {
		kind = "reaction_removed"
	}
	slack.push(t, app, map[string]any{"type": "events_api",
		"envelope_id":              fmt.Sprintf("%s:%s:%s:%s:%s", app, kind, user, name, ts),
		"accepts_response_payload": false,
		"payload": map[string]any{"type": "event_callback", "team_id": "T0ACME", "event": map[string]any{
			"type": kind, "user": user, "reaction": name, "event_ts": ts,
			"item": map[string]any{"type": "message", "channel": channel, "ts": ts}}}})
}

// waitReacted waits for an app to set want.
func (slack *fakeSlack) waitReacted(t *testing.T, want slackReaction) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		slack.mu.Lock()
		found := slices.Contains(slack.reacted, want)
		slack.mu.Unlock()
		if found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	slack.mu.Lock()
	defer slack.mu.Unlock()
	t.Fatalf("no app set %+v; apps set %+v", want, slack.reacted)
}

// A person's Slack reaction reaches the hub as the emoji it draws, once,
// although every loop's app in the channel hears it; a custom emoji is kept
// by its name, and a stranger's is recorded nowhere. A loop's reaction is
// set by its own app, by Slack's name for the emoji (ADR-0040).
func TestSlackReactionsTravelBothWays(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const morning = "1727600000.000010"
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", morning)
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })
	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "@milo the build is green"}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	post := slack.waitPost(t, "the build is green")
	sent := srv.waitGroupMessage("@milo the build is green", mirrorIs("mirrored"))
	person := "slack:" + slackOperator

	slack.pushReaction(t, slackAppToken, "U0STRANGER", "-1", slackChannel, post.TS, false)
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushReaction(t, app, slackOperator, "+1::skin-tone-3", slackChannel, post.TS, false)
	}
	srv.waitReactions(sent.ID, person+" 👍🏼")
	slack.pushReaction(t, slackAppToken, slackOperator, "partyparrot", slackChannel, post.TS, false)
	srv.waitReactions(sent.ID, person+" 👍🏼", person+" :partyparrot:")
	slack.pushReaction(t, slackAppToken, slackOperator, "+1::skin-tone-3", slackChannel, post.TS, true)
	srv.waitReactions(sent.ID, person+" :partyparrot:")

	morningMessage := srv.waitGroupMessage("morning", func(activityMessage) bool { return true })
	ref := fmt.Sprintf("ref:%d", morningMessage.ID)
	for _, emoji := range []string{"🎉", ":partyparrot:"} {
		if res := callSend(t, terra, map[string]any{"destination": "group", "reply_to": ref, "react": emoji}); res.IsError {
			t.Fatalf("terra's %s refused: %s", emoji, resultText(res))
		}
	}
	slack.waitReacted(t, slackReaction{Token: slackBotToken, Channel: slackChannel, TS: morning, Name: "tada"})
	slack.waitReacted(t, slackReaction{Token: slackBotToken, Channel: slackChannel, TS: morning, Name: "partyparrot"})
}
