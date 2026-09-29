//go:build integration

package itest

import (
	"fmt"
	"testing"
)

// A loop's group send is its own app's post in the channel the app is bound
// to, with a mention of another loop written as Slack's mention of its app
// (#230). A human's reply in the post's thread is a reply to the loop's
// message and reaches it; the loop's answer to that goes in the same
// thread. A post Slack will not take is a failure the operator can retry,
// and a loop whose app is bound to no channel keeps its words on the hub.
func TestSlackGroupSendIsTheAppsPost(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	// terra's app binds to the channel an allowed sender speaks in; milo's
	// hears nothing, so it is bound to none
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "@milo terra's words"}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	post := slack.waitPost(t, "terra's words")
	if post.Token != slackBotToken || post.Channel != slackChannel || post.Text != "<@U0MILO> terra's words" || post.ThreadTS != "" {
		t.Fatalf("terra's words were posted as %+v", post)
	}
	sent := srv.waitGroupMessage("@milo terra's words", mirrorIs("mirrored"))

	slack.pushReply(t, slackAppToken, "channel", slackChannel, slackOperator, "why?", "1727600000.000020", post.TS)
	reply := srv.waitGroupMessage("why?", func(m activityMessage) bool { return m.ReplyToID != 0 })
	if reply.ReplyToID != sent.ID {
		t.Fatalf("a reply in the post's thread answers message %d, want terra's %d", reply.ReplyToID, sent.ID)
	}
	srv.waitInput("terra", "why?", fmt.Sprintf("in reply to ref:%d", sent.ID))

	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "because",
		"reply_to": fmt.Sprintf("ref:%d", reply.ID)}); res.IsError {
		t.Fatalf("terra's reply refused: %s", resultText(res))
	}
	if answer := slack.waitPost(t, "because"); answer.ThreadTS != post.TS {
		t.Fatalf("terra's reply went to thread %q, want its post's %q", answer.ThreadTS, post.TS)
	}

	slack.setFailPosts(-1)
	if res := callSend(t, terra, map[string]any{"destination": "group", "text": "@milo words that fail"}); res.IsError {
		t.Fatalf("terra's group send refused: %s", resultText(res))
	}
	failed := srv.waitGroupMessage("@milo words that fail", func(m activityMessage) bool { return m.SendFailedAt != 0 })
	slack.setFailPosts(0)
	srv.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", failed.ID), nil, nil)
	slack.waitPost(t, "words that fail")
	srv.waitGroupMessage("@milo words that fail", mirrorIs("mirrored"))

	milo := mcpSession(t, srv, hubMCPToken(t, srv, "milo"))
	if res := callSend(t, milo, map[string]any{"destination": "group", "text": "@terra milo's app is in no channel"}); res.IsError {
		t.Fatalf("milo's group send refused: %s", resultText(res))
	}
	srv.waitGroupMessage("@terra milo's app is in no channel", mirrorIs("not_mirrored"))
	for _, taken := range slack.postsTaken() {
		if taken.Token == slackMiloBotToken {
			t.Fatalf("milo's app posted with no channel bound: %+v", taken)
		}
	}
}
