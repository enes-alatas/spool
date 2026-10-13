//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A send Telegram posts but answers too late reads, to the hub, like a dead
// connection: the bridge retries it, and the Bot API offers no way to ask
// whether the first attempt landed. The message arrives twice. What the hub
// owes is to say so: the log names the send that may have arrived twice,
// and nothing is reported undelivered (#800).
func TestASendAnsweredTooLateIsLoggedAsAPossibleDuplicate(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7800, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta said to a slow Telegram"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.lateNextSends(1)
	srv.message("alpha", "say it")
	tg.waitSentFrom(t, groupChatID, "alpha", "said to a slow Telegram")
	const warning = "may have arrived twice"
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(srv.log(), warning) {
		if time.Now().After(deadline) {
			t.Fatalf("no log line says the send may have arrived twice; log:\n%s", srv.log())
		}
		time.Sleep(100 * time.Millisecond)
	}
	var landed int
	for _, m := range tg.sentTo(groupChatID) {
		if m.Text == "@beta said to a slow Telegram" {
			landed++
		}
	}
	if landed != 2 {
		t.Fatalf("the message landed %d times, want twice: the late attempt and its retry", landed)
	}
	if srv.hasEvent("alpha", "send_failed", 2*time.Second) {
		t.Fatal("a send that landed was reported failed")
	}
	if rows := srv.undelivered(); len(rows) != 0 {
		t.Fatalf("a send that landed is listed undelivered: %s", dump(rows))
	}
}
