//go:build integration

package itest

import (
	"strings"
	"testing"
)

// A channel other than the fleet channel has no room on any surface yet, so
// a loop's words there stay on the hub, even from a loop whose bot sits in
// the fleet channel's room (ADR-0038). The fleet send after it is the
// barrier: the surface carries a loop's sends in order, so once that one is
// posted the channel's would have been too.
func TestAChannelSendStaysOffTelegram(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	for _, send := range []map[string]any{
		{"destination": "channel:backend", "text": "@beta backend only"},
		{"destination": "group", "text": "@beta fleet marker"},
	} {
		if res := callSend(t, alpha, send); res.IsError {
			t.Fatalf("send %v refused: %s", send, resultText(res))
		}
	}
	tg.waitSentFrom(t, groupChatID, "alpha", "fleet marker")
	if sent, ok := tg.sentAnywhere("backend only"); ok {
		t.Fatalf("a channel's message reached Telegram: %+v", sent)
	}
	for _, m := range srv.activity() {
		if m.Text == "@beta backend only" && m.Mirror != "not_mirrored" {
			t.Errorf("the channel's message is not kept on the hub: %s", dump(m))
		}
	}
}

// The same on Slack.
func TestAChannelSendStaysOffSlack(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, slackOperator, "morning", "1727600000.000010")
	srv.waitSlackLink("terra", func(link slackStatus) bool { return link.ChannelID == slackChannel })
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"terra", "milo"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	for _, send := range []map[string]any{
		{"destination": "channel:backend", "text": "@milo backend only"},
		{"destination": "group", "text": "@milo fleet marker"},
	} {
		if res := callSend(t, terra, send); res.IsError {
			t.Fatalf("send %v refused: %s", send, resultText(res))
		}
	}
	slack.waitPost(t, "fleet marker")
	for _, taken := range slack.postsTaken() {
		if strings.Contains(taken.Text, "backend only") {
			t.Fatalf("a channel's message reached Slack: %+v", taken)
		}
	}
}

// No person is in a channel until a room mirrors it, so a channel send
// that names only a person reaches nobody and is refused, with where people
// are; the same words in the fleet channel reach its room (ADR-0038).
func TestAChannelSendToAPersonIsRefused(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	res := callSend(t, alpha, map[string]any{"destination": "channel:backend", "text": "@operator please merge"})
	wantSendError(t, res, "no_recipients")
	if !strings.Contains(resultText(res), "no person is in channel:backend") || !strings.Contains(resultText(res), "group") {
		t.Errorf("the refusal does not say where people are: %s", resultText(res))
	}
	if res := callSend(t, alpha, map[string]any{"destination": "channel:backend", "text": "@operator @beta please look"}); res.IsError {
		t.Errorf("a channel send naming a loop in it as well was refused: %s", resultText(res))
	}
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": "@operator please merge"}); res.IsError {
		t.Fatalf("the fleet channel refused a person: %s", resultText(res))
	}
	tg.waitSentFrom(t, groupChatID, "alpha", "please merge")
}
