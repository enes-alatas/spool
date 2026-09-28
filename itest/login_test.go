//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A login the API refuses fails every turn the same way, and until #405 each
// one was an ordinary errored turn: the loop looked alive, did nothing, and
// dropped what it was told. Now the loop is down for a named reason, the one
// fix the operator has is in the alert, and what it was told waits for the
// login to come back. Logging in again happens outside Spool, so the loop
// finds out by retrying, and the alert clears on the first turn that runs.
func TestARejectedLoginIsNamedAndTheLoopsWorkWaitsForIt(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	expired := filepath.Join(s.fkState, "login-expired")
	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.createLoop("aster", nil)

	s.waitState("aster", "workstation_down", 30*time.Second)
	v := s.loop("aster")
	if v.DownReason != "unauthenticated" {
		t.Fatalf("down_reason = %q, want unauthenticated", v.DownReason)
	}
	for _, want := range []string{"OAuth session expired", "log in again with claude on the host"} {
		if !strings.Contains(v.WorkstationDetail, want) {
			t.Fatalf("workstation_detail = %q, want it to carry %q", v.WorkstationDetail, want)
		}
	}

	// What the loop is told while its login is refused is not handed to a
	// turn that never reaches the model: it is refused too, and kept.
	sent := time.Now().UnixMilli()
	before := newestEventID(s.eventsQuery("aster", "limit=200"))
	s.message("aster", "are you there")
	s.waitTurn("aster", 60*time.Second, func(tn turn) bool {
		return tn.IsError && tn.StartedAt >= sent
	})
	// The creation tick's refusal scheduled a retry already; this one has
	// to be the message's.
	retried := false
	for deadline := time.Now().Add(5 * time.Second); !retried && time.Now().Before(deadline); {
		for _, e := range s.eventsQuery("aster", "limit=200") {
			retried = retried || (e.Subtype == "retry_scheduled" && e.ID > before)
		}
		if !retried {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !retried {
		t.Fatal("a refused message scheduled no retry; nothing would notice the login coming back")
	}
	if v = s.loop("aster"); v.DownReason != "unauthenticated" {
		t.Fatalf("down_reason = %q after a retry's spawn, want the alert held until a turn runs", v.DownReason)
	}

	// Nothing new arrives from here on: only the retry can find the login
	// back.
	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	s.waitTurn("aster", 60*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "are you there")
	})
	if v = s.loop("aster"); v.DownReason != "" || v.State == "workstation_down" {
		t.Fatalf("after a turn ran: state %q, down_reason %q, want the alert cleared", v.State, v.DownReason)
	}
	if !s.hasEvent("aster", "workstation_up", 5*time.Second) {
		t.Fatal("the alert cleared without a workstation_up event on the loop's timeline")
	}
}

// newestEventID is the highest event id in events, 0 when there are none.
// Ids only grow, so what comes after it is what happened since.
func newestEventID(events []spoolEvent) int64 {
	var newest int64
	for _, e := range events {
		newest = max(newest, e.ID)
	}
	return newest
}

// Nobody watching the control room learns of a refused login from the alert
// alone: on 2026-09-27 the fleet stood still for two hours and the operator
// found out from the silence (#419). So the hub tells the owner in their
// private chat, once for the whole outage rather than once per loop the
// shared login stopped, and once more when it works again, in the same chat.
func TestARejectedLoginIsToldToTheOwnerOnce(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6464, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	for _, name := range []string{"alpha", "beta"} {
		tg.dm(name, operator, "hi "+name)
		waitOwnerDMReady(t, srv, name)
		srv.waitTurn(name, 30*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "hi "+name)
		})
	}

	expired := filepath.Join(srv.fkState, "login-expired")
	if err := os.MkdirAll(srv.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expired, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		srv.message(name, "are you there, "+name)
	}
	for _, name := range []string{"alpha", "beta"} {
		srv.waitState(name, "workstation_down", 30*time.Second)
	}
	const refusedText = "the Claude login was refused"
	notice := tg.waitSent(t, operator.ID, refusedText)
	for _, want := range []string{"OAuth session expired", "running claude on the host", "Every bare loop"} {
		if !strings.Contains(notice.Text, want) {
			t.Fatalf("the owner was told %q, want it to carry %q", notice.Text, want)
		}
	}
	// Both loops were refused before the wait above ended; give the second
	// refusal's notice the time to go out, if it were going to.
	time.Sleep(2 * time.Second)
	if n := countSent(tg, operator.ID, refusedText); n != 1 {
		t.Fatalf("the owner was told of one refused login %d times, want once", n)
	}
	if !strings.Contains(notice.Text, notice.Token+" has stopped") {
		t.Fatalf("notice sent by bot %q names another loop: %q", notice.Token, notice.Text)
	}
	if !srv.hasEvent(notice.Token, "owner_notice", 5*time.Second) {
		t.Fatalf("%s told the owner without an owner_notice on its timeline", notice.Token)
	}
	for _, m := range srv.activity() {
		if strings.Contains(m.Text, refusedText) {
			t.Fatalf("the hub's notice was recorded as a message: %s", dump(m))
		}
	}

	if err := os.Remove(expired); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		srv.waitTurn(name, 60*time.Second, func(tn turn) bool {
			return !tn.IsError && strings.Contains(tn.ResultText, "are you there, "+name)
		})
	}
	const worksText = "the Claude login works again"
	tg.waitSentFrom(t, operator.ID, notice.Token, worksText)
	time.Sleep(2 * time.Second)
	if n := countSent(tg, operator.ID, worksText); n != 1 {
		t.Fatalf("the owner was told the login works again %d times, want once", n)
	}
}

// countSent is how many messages any bot has posted to chatID containing text.
func countSent(tg *fakeTelegram, chatID int64, text string) int {
	n := 0
	for _, m := range tg.sentTo(chatID) {
		if strings.Contains(m.Text, text) {
			n++
		}
	}
	return n
}
