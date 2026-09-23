//go:build integration

package itest

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// groupMessage is the fleet channel's message with exactly this text.
func (s *server) groupMessage(text string) (activityMessage, bool) {
	s.t.Helper()
	var timeline []activityMessage
	s.mustJSON("GET", "/api/group?limit=200", nil, &timeline)
	for _, m := range timeline {
		if m.Text == text {
			return m, true
		}
	}
	return activityMessage{}, false
}

// waitGroupMessage blocks until the fleet channel's message with this text
// satisfies pred, and returns it.
func (s *server) waitGroupMessage(text string, pred func(activityMessage) bool) activityMessage {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last activityMessage
	for time.Now().Before(deadline) {
		if m, ok := s.groupMessage(text); ok {
			if last = m; pred(m) {
				return m
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("group message %q never reached the expected state; last seen %s", text, dump(last))
	return last
}

func mirrorIs(want string) func(activityMessage) bool {
	return func(m activityMessage) bool { return m.Mirror == want }
}

// Every message says whether it is on the surface too (ADR-0032 item 6), as
// a spelled state: what came in from Telegram is on it; a loop's send is
// pending until the bridge's send lands and mirrored after; the operator's
// words and a surface-less loop's send stay on the hub. A failed send is
// pending with the failure on the send fields, and a retry that lands
// mirrors it.
func TestMirrorSaysWhereAMessageIs(t *testing.T) {
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	srv.createLoop("gamma", nil)

	tg.post(groupChatID, "supergroup", "@alpha a human in telegram", operator)
	srv.waitGroupMessage("@alpha a human in telegram", mirrorIs("mirrored"))

	srv.mustJSON("POST", "/api/group", map[string]any{"text": "@beta the operator's words"}, nil)
	srv.waitGroupMessage("@beta the operator's words", mirrorIs("not_mirrored"))

	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": "@beta alpha's words"}); res.IsError {
		t.Fatalf("alpha's group send refused: %s", resultText(res))
	}
	tg.waitSentFrom(t, groupChatID, "alpha", "alpha's words")
	srv.waitGroupMessage("@beta alpha's words", mirrorIs("mirrored"))

	gamma := mcpSession(t, srv, hubMCPToken(t, srv, "gamma"))
	if res := callSend(t, gamma, map[string]any{"destination": "group", "text": "@alpha gamma has no bot"}); res.IsError {
		t.Fatalf("gamma's group send refused: %s", resultText(res))
	}
	srv.waitGroupMessage("@alpha gamma has no bot", mirrorIs("not_mirrored"))

	tg.failNextSends(-1)
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": "@beta words that fail"}); res.IsError {
		t.Fatalf("alpha's group send refused: %s", resultText(res))
	}
	failed := srv.waitGroupMessage("@beta words that fail", func(m activityMessage) bool { return m.SendFailedAt != 0 })
	if failed.Mirror != "pending" {
		t.Fatalf("a failed send reads mirror %q, want pending with the failure on the send fields", failed.Mirror)
	}
	tg.failNextSends(0)
	srv.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", failed.ID), nil, nil)
	srv.waitGroupMessage("@beta words that fail", mirrorIs("mirrored"))

	for _, m := range srv.activity() {
		if m.Mirror == "" {
			t.Fatalf("a message carries no mirror answer: %s", dump(m))
		}
		if m.Conversation == "control_room" && m.Mirror != "not_mirrored" {
			t.Fatalf("a control room message reads mirror %q: %s", m.Mirror, dump(m))
		}
	}
}

// A send the hub was stopped in the middle of is a failure when it starts
// again, not a message in flight forever: the send queue lived in the
// stopped process, so nothing would ever send it. As a failure it is on the
// operator's list, retryable, and on its loop's timeline.
func TestSendInterruptedByARestartIsAFailure(t *testing.T) {
	operator := user{ID: 5252, First: "Operator", Username: "operator"}
	dir := t.TempDir()
	srv, tg := startTelegramFleetIn(t, dir, operator)

	// Refused sends are retried with a backoff of seconds, which is the
	// window the hub is stopped in: the row is written and pending, and no
	// failure has been recorded yet.
	tg.failNextSends(-1)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const text = "@beta cut off mid-send"
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": text}); res.IsError {
		t.Fatalf("alpha's group send refused: %s", resultText(res))
	}
	deadline := time.Now().Add(10 * time.Second)
	for tg.sendAttempts() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if tg.sendAttempts() == 0 {
		t.Fatal("the bridge never attempted the send")
	}
	if m, _ := srv.groupMessage(text); m.Mirror != "pending" || m.SendFailedAt != 0 {
		t.Fatalf("before the restart the send reads %s, want pending with no failure yet", dump(m))
	}
	srv.stop()
	tg.failNextSends(0)

	srv2 := startTelegramServer(t, dir, tg)
	m, ok := srv2.groupMessage(text)
	if !ok {
		t.Fatal("the interrupted send is gone after the restart")
	}
	if m.Mirror != "pending" || m.SendFailedAt == 0 || !strings.Contains(m.SendError, "hub stopped") {
		t.Fatalf("after the restart the send reads %s, want a pending failure saying the hub stopped with it unsent", dump(m))
	}
	if !srv2.hasEvent("alpha", "send_failed", 10*time.Second) {
		t.Fatal("the interrupted send is not on alpha's timeline")
	}
	srv2.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", m.ID), nil, nil)
	tg.waitSentFrom(t, groupChatID, "alpha", "cut off mid-send")
	srv2.waitGroupMessage(text, mirrorIs("mirrored"))
}
