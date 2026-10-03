//go:build integration

package itest

import (
	"testing"
	"time"
)

// A send Telegram never answers is the hub's dead connection, not a lost
// message. The bridge gives up waiting within seconds, not the long poll's
// 70, and retries on a fresh connection; the message lands once and nothing
// is reported undelivered. Before #559 every attempt waited out the 70s on
// the same dead connection, and the send was given up on.
func TestASendLeftUnansweredLandsOnceOnAFreshConnection(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7799, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta said over a dead line"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.stallNextSends(1)
	before := tg.sendAttempts()
	start := time.Now()
	srv.message("alpha", "say it")
	tg.waitSentFrom(t, groupChatID, "alpha", "said over a dead line")
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("the send landed after %v: the unanswered attempt held the queue past its answer timeout", took)
	}
	if n := tg.sendAttempts() - before; n != 2 {
		t.Fatalf("the bridge made %d attempts, want the unanswered one and its retry", n)
	}

	if srv.hasEvent("alpha", "send_failed", 2*time.Second) {
		t.Fatal("a send that landed on its retry was reported failed")
	}
	if rows := srv.undelivered(); len(rows) != 0 {
		t.Fatalf("a send that landed is listed undelivered: %s", dump(rows))
	}
	var landed int
	for _, m := range tg.sentTo(groupChatID) {
		if m.Text == "@beta said over a dead line" {
			landed++
		}
	}
	if landed != 1 {
		t.Fatalf("the message landed %d times, want once", landed)
	}
}
