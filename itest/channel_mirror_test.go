//go:build integration

package itest

import (
	"strings"
	"testing"
)

// A channel other than fleet has no room on any surface yet, so
// a loop's words there stay on the hub, even from a loop whose bot sits in
// fleet's room (ADR-0038). The fleet send after it is the
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
		{"destination": "channel:fleet", "text": "@beta fleet marker"},
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
	srv.waitRoom("terra", slackChannel)
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"terra", "milo"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	terra := mcpSession(t, srv, hubMCPToken(t, srv, "terra"))
	for _, send := range []map[string]any{
		{"destination": "channel:backend", "text": "@milo backend only"},
		{"destination": "channel:fleet", "text": "@milo fleet marker"},
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

// A known person counts in every channel, mirrored or not, since every
// channel's timeline is in the control room (ADR-0068): a channel send that
// names only a person is kept and wakes no loop, and the same words in a
// channel a room mirrors reach that room too.
func TestAChannelSendToAPersonIsKept(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"alpha", "beta"} {
		srv.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}

	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	if res := callSend(t, alpha, map[string]any{"destination": "channel:backend", "text": "@operator merge backend"}); res.IsError {
		t.Fatalf("a channel send naming a person was refused: %s", resultText(res))
	}
	if got := srv.activityWith("@operator merge backend"); len(got) != 1 || len(got[0].DeliveredTo) != 0 || got[0].Channel != "backend" {
		t.Fatalf("the person's channel send = %s, want one message in backend delivered to no loop", dump(got))
	}
	if res := callSend(t, alpha, map[string]any{"destination": "channel:backend", "text": "@operator @beta please look"}); res.IsError {
		t.Errorf("a channel send naming a loop in it as well was refused: %s", resultText(res))
	}
	if res := callSend(t, alpha, map[string]any{"destination": "channel:fleet", "text": "@operator please merge"}); res.IsError {
		t.Fatalf("fleet refused a person: %s", resultText(res))
	}
	// The mirror consumes in order, so a private message to the operator
	// for backend's send would have gone out before fleet's post did:
	// counting the person in backend is not delivering to them (ADR-0068).
	tg.waitSentFrom(t, groupChatID, "alpha", "please merge")
	if sent, ok := tg.sentAnywhere("merge backend"); ok {
		t.Fatalf("a send in a channel with no room reached the person it named (chat %d, bot %q): %q", sent.ChatID, sent.Token, sent.Text)
	}
}
