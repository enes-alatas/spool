//go:build integration

package itest

import (
	"testing"
	"time"
)

// Telegram's front proxy can answer 502 to a send the Bot API goes on to
// make. Retried, an owner's notice arrived once per attempt, so one open
// and one clear read as the alert going off again and again (#1584). The
// owner's chat here posts every send and answers it 502: the notice that
// the problem opened and the one that it cleared each arrive once, and the
// loop's own resend, to a chat that answers, still lands.
func TestAProblemNoticeAnswered502ArrivesOnce(t *testing.T) {
	t.Parallel()
	operator := user{ID: 15840, First: "Operator", Username: "operator"}
	workspace := workspaceWithScript(t, "ok\nok\n"+
		`!send {"destination":"channel:fleet","text":"@operator the build is stuck"}`+"\n"+
		`!send {"destination":"channel:fleet","text":"@operator saying it again","resends":"$ref"}`+"\n"+
		"!echo\n")
	hub, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspace})
	hub.waitCompleted("alpha", 1)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, hub, "alpha")
	hub.waitCompleted("alpha", 2)
	hub.mustJSON("PUT", "/api/settings", map[string]any{"health_send_grace_minutes": 1}, nil)

	tg.failSendsContaining("the build is stuck")
	hub.message("alpha", "say it")
	if !hub.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	tg.failSendsContaining("")
	tg.landThenFailSendsTo(operator.ID, true)

	const opened, cleared = "a message alpha sent at", "alpha has no undelivered messages left"
	within(t, 120*time.Second, "alpha's send_failed problem", func() bool {
		return hub.loop("alpha").Health == "warning"
	})
	tg.waitSentFrom(t, operator.ID, "alpha", opened)
	tg.waitSent(t, groupChatID, "saying it again")
	tg.waitSentFrom(t, operator.ID, "alpha", cleared)
	// past the ~7s of backoff a retried send would spend on its attempts
	time.Sleep(9 * time.Second)
	for _, notice := range []string{opened, cleared} {
		if told := countSent(tg, operator.ID, notice); told != 1 {
			t.Errorf("the owner got %q %d times, want once", notice, told)
		}
	}
}
