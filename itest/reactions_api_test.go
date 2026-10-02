//go:build integration

package itest

import (
	"strconv"
	"testing"
)

// The control room reads a message's reactions with the message, on every
// route it reads messages from, and a message nobody reacted to carries
// none (ADR-0040, #535).
func TestTheControlRoomReadsAMessagesReactions(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9191, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const post, quiet = "@beta the build is green", "@beta and the docs too"
	for _, text := range []string{post, quiet} {
		if res := callSend(t, alpha, map[string]any{"destination": "group", "text": text}); res.IsError {
			t.Fatalf("send refused: %s", resultText(res))
		}
	}
	sent := tg.waitSentFrom(t, groupChatID, "alpha", post)
	srv.waitForMessage(quiet)
	messageID := srv.activityWith(post)[0].ID
	person := "telegram:" + strconv.FormatInt(operator.ID, 10)
	tg.react("alpha", groupChatID, sent.MessageID, operator, nil, []string{"👍"})
	srv.waitReactions(messageID, person+" 👍")

	type reactionView struct {
		MessageID  int64  `json:"message_id"`
		ReactorKey string `json:"reactor_key"`
		Reactor    string `json:"reactor"`
		Emoji      string `json:"emoji"`
		TS         int64  `json:"ts"`
	}
	for _, path := range []string{"/api/activity?limit=200", "/api/group", "/api/channels/group/messages"} {
		var msgs []struct {
			ID        int64           `json:"id"`
			Text      string          `json:"text"`
			Reactions *[]reactionView `json:"reactions"`
		}
		srv.mustJSON("GET", path, nil, &msgs)
		seen := 0
		for _, msg := range msgs {
			switch msg.Text {
			case post:
				seen++
				if msg.Reactions == nil || len(*msg.Reactions) != 1 {
					t.Fatalf("%s: the reacted message reads %s", path, dump(msg))
				}
				got := (*msg.Reactions)[0]
				if got.MessageID != messageID || got.ReactorKey != person || got.Emoji != "👍" || got.Reactor == "" || got.TS == 0 {
					t.Errorf("%s: the reaction reads %+v", path, got)
				}
			case quiet:
				seen++
				if msg.Reactions != nil {
					t.Errorf("%s: a message with no reactions carries %s", path, dump(*msg.Reactions))
				}
			}
		}
		if seen != 2 {
			t.Errorf("%s: found %d of the two posts in %s", path, seen, dump(msgs))
		}
	}
}
