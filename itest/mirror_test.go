//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"path/filepath"
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

// Every message says whether it is on the surface too (ADR-0032), as
// a spelled state: what came in from Telegram is on it; a loop's send is
// pending until the bridge's send lands and mirrored after; the operator's
// words and a surface-less loop's send stay on the hub. A failed send is
// pending with the failure on the send fields, and a retry that lands
// mirrors it.
func TestMirrorSaysWhereAMessageIs(t *testing.T) {
	t.Parallel()
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

// A send the hub is stopped in the middle of is a failure as the hub stops,
// not a message in flight forever: the send queue lives in the stopping
// process, so nothing would ever send it. It is failed before the store
// closes (ADR-0036), so the record does not wait for a next start that may
// never come. As a failure it is on the operator's list, retryable, and on
// its loop's timeline.
func TestSendInterruptedByAShutdownIsAFailure(t *testing.T) {
	t.Parallel()
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
		t.Fatalf("before the shutdown the send reads %s, want pending with no failure yet", dump(m))
	}
	srv.stop()
	tg.failNextSends(0)

	// read with the hub down: the failure is on the row and the timeline
	// already, written by the hub that stopped
	failedAt, sendErr, events := storedSendFailure(t, dir, text)
	if failedAt == 0 || !strings.Contains(sendErr, "hub stopped") || events != 1 {
		t.Fatalf("after the shutdown the send reads failed_at=%d error=%q with %d send_failed events, want one failure saying the hub stopped with it unsent",
			failedAt, sendErr, events)
	}

	srv2 := startTelegramServer(t, dir, tg)
	m, ok := srv2.groupMessage(text)
	if !ok {
		t.Fatal("the interrupted send is gone after the restart")
	}
	if m.Mirror != "pending" || m.SendFailedAt != failedAt {
		t.Fatalf("after the restart the send reads %s, want the failure recorded at the shutdown, unchanged", dump(m))
	}
	if strings.Contains(srv2.log(), "send unsettled at startup") {
		t.Fatalf("the next start found the send unsettled; the shutdown should have settled it:\n%s", srv2.log())
	}
	srv2.mustJSON("POST", fmt.Sprintf("/api/messages/%d/retry", m.ID), nil, nil)
	tg.waitSentFrom(t, groupChatID, "alpha", "cut off mid-send")
	srv2.waitGroupMessage(text, mirrorIs("mirrored"))
}

// storedSendFailure reads a send's failure straight from spool.db, with the
// hub down: when it failed, why, and how many send_failed events its loop's
// timeline holds.
func storedSendFailure(t *testing.T, dataDir, text string) (failedAt int64, sendErr string, events int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var loopID string
	if err := db.QueryRow(`SELECT send_failed_at, send_error, from_loop_id FROM messages WHERE text=?`, text).
		Scan(&failedAt, &sendErr, &loopID); err != nil {
		t.Fatalf("read the send %q: %v", text, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM events WHERE loop_id=? AND subtype='send_failed'`, loopID).
		Scan(&events); err != nil {
		t.Fatalf("count send_failed events: %v", err)
	}
	return failedAt, sendErr, events
}
