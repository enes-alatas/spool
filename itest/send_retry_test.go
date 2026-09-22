//go:build integration

package itest

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (s *server) undelivered() []undeliveredRow {
	s.t.Helper()
	var rows []undeliveredRow
	s.mustJSON("GET", "/api/undelivered", nil, &rows)
	return rows
}

// waitUndelivered waits for the fleet-wide list to hold n rows, since a retry
// is queued rather than performed inside the request (#269).
func (s *server) waitUndelivered(n int, timeout time.Duration) []undeliveredRow {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	var rows []undeliveredRow
	for time.Now().Before(deadline) {
		rows = s.undelivered()
		if len(rows) == n {
			return rows
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.t.Fatalf("undelivered list held %d rows, want %d: %s", len(rows), n, dump(rows))
	return nil
}

// The operator's half of a lost send: say it again, and if it lands this
// time, stop being told about it. Before #269 nothing could resolve a
// failure — the count only fell when a 24-hour window slid past it, which is
// a clock deciding when the operator is done looking.
func TestRetryingALostSendResolvesIt(t *testing.T) {
	operator := user{ID: 7766, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	rows := srv.waitUndelivered(1, 30*time.Second)
	lost := rows[0]
	if n := srv.loop("alpha").Undelivered; n != 1 {
		t.Fatalf("the badge counts %d, want 1", n)
	}

	// Telegram is back. The retry is the same row to the same destination,
	// so what lands is the message that was lost, not a new one.
	tg.failNextSends(0)
	before := tg.sendAttempts()
	srv.mustJSON("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/retry", nil, nil)

	if got := srv.waitUndelivered(0, 30*time.Second); len(got) != 0 {
		t.Fatalf("the list still holds the retried failure: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("the badge still counts %d after a successful retry", n)
	}
	if tg.sendAttempts() <= before {
		t.Fatal("no send was attempted: the retry resolved the row without sending")
	}

	// Resolved is not delivered: what failed and why is still on the row,
	// because the loop's timeline says it failed and the store must not
	// disagree with it.
	var row activityMessage
	for _, m := range srv.activity() {
		if m.ID == lost.ID {
			row = m
		}
	}
	if row.SendFailedAt == 0 || row.SendError == "" {
		t.Errorf("the resolved row lost its failure: failed_at %d, error %q", row.SendFailedAt, row.SendError)
	}
	if row.SendResolvedAt == 0 {
		t.Error("the resolved row records no resolution time")
	}

	// And a second retry of the same message is gone from under them.
	resp, body := srv.do("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/retry", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second retry: status = %d, want 404 (%s)", resp.StatusCode, body)
	}
}

// Dismiss is the other way out: the operator has read the failure and is done
// with it, and nothing is sent.
func TestDismissingALostSendResolvesItWithoutSending(t *testing.T) {
	operator := user{ID: 7767, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta said into a dead line"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]

	tg.failNextSends(0) // a send now would succeed, so a send is visible if one happens
	before := tg.sendAttempts()
	srv.mustJSON("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/dismiss", nil, nil)

	if got := srv.undelivered(); len(got) != 0 {
		t.Fatalf("the list still holds the dismissed failure: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("the badge still counts %d after a dismissal", n)
	}
	if tg.sendAttempts() != before {
		t.Fatal("a dismissal sent the message: it is supposed to send nothing")
	}

	resp, body := srv.do("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/dismiss", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second dismissal: status = %d, want 404 (%s)", resp.StatusCode, body)
	}
}

// A message that never failed is not retryable, whatever id the caller has.
func TestRetryingADeliveredMessageIsNotFound(t *testing.T) {
	operator := user{ID: 7768, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)

	srv.message("alpha", "this one arrives")
	rows := srv.activity()
	if len(rows) == 0 {
		t.Fatal("no messages in activity to try")
	}
	for _, path := range []string{"retry", "dismiss"} {
		resp, body := srv.do("POST", "/api/messages/"+strconv.FormatInt(rows[0].ID, 10)+"/"+path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s of a delivered message: status = %d, want 404 (%s)", path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "no unresolved send failure") {
			t.Errorf("%s refusal does not say why: %s", path, body)
		}
	}
}

// A retry that lands before the sender's next wake must also cancel the news
// owed to it. The loop is told a send was lost so that it can decide whether
// the words are still worth saying (ADR-0026, 2026-09-18 amendment) — told
// that about a message the operator just delivered, a well-behaved loop says
// it again and the human reads it twice, which is the doubling the amendment
// exists to prevent, arriving from the other side.
func TestARetriedSendIsNotToldToItsSenderAsLost(t *testing.T) {
	operator := user{ID: 7769, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]
	// The loop must not have woken since, or it would already be told.
	srv.waitState("alpha", "asleep", 60*time.Second)

	tg.failNextSends(0)
	srv.mustJSON("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/retry", nil, nil)
	if got := srv.waitUndelivered(0, 30*time.Second); len(got) != 0 {
		t.Fatalf("the retry never resolved the row: %s", dump(got))
	}

	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	next := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "anything new")
	})
	if strings.Contains(next.ResultText, "never arrived") {
		t.Fatalf("the loop was told a message it in fact delivered was lost:\n%s", next.ResultText)
	}
}

// The one refusal of a retry the operator can act on: a private message whose
// loop has no owner chat to deliver it to. It is a 409 rather than a 404 —
// the failure is still there and still theirs to deal with — and the row is
// left unresolved, because nothing was sent.
func TestRetryingAPrivateMessageWithNoChatIsRefused(t *testing.T) {
	operator := user{ID: 7770, First: "Operator", Username: "operator"}
	colleague := user{ID: 7771, First: "Colleague", Username: "colleague"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"owner_dm","text":"the deploy is wedged"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// The owner's DM is captured from their message, so the send has a chat
	// to fail against; Telegram is refusing everything by the time it runs.
	tg.failNextSends(-1)
	tg.dm("alpha", operator, "hi")
	waitOwnerDMReady(t, srv, "alpha")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the private send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]

	// The loop changes hands. The captured chat belonged to the previous
	// owner, so it is dropped, and the new owner has not written to this bot
	// yet — there is nowhere for the retry to land.
	tg.dm("alpha", colleague, "knock")
	srv.allowSender(colleague.ID)
	srv.mustJSON("PUT", "/api/loops/alpha/owner", map[string]any{"tg_user_id": colleague.ID}, nil)

	tg.failNextSends(0) // a send now would succeed, so a send is visible if one happens
	before := tg.sendAttempts()
	resp, body := srv.do("POST", "/api/messages/"+strconv.FormatInt(lost.ID, 10)+"/retry", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("retry with no owner chat: status = %d, want 409 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "private chat") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}
	if tg.sendAttempts() != before {
		t.Error("a refused retry still attempted a send")
	}

	// And it is still the operator's to deal with: a refusal is not a
	// resolution, so the failure stays on the list and in the count.
	if got := srv.undelivered(); len(got) != 1 || got[0].ID != lost.ID {
		t.Fatalf("the refused retry changed the list: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 1 {
		t.Errorf("the badge counts %d after a refused retry, want 1", n)
	}
}
