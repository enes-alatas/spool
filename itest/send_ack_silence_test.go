//go:build integration

package itest

import (
	"testing"
	"time"
)

// Acknowledging a loop's send_failed alert silences the failures it
// covered for the acknowledgement's period, an hour by default, rather
// than starting the grace over; the owner's notice says until when, and a
// hub restart during the silence keeps it (#1588).
func TestAnAcknowledgedUndeliveredSendStaysQuietAcrossARestart(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7979, First: "Operator", Username: "operator"}
	dataDir := t.TempDir()
	workspace := workspaceWithScript(t, "ok\nok\n"+
		`!send {"destination":"channel:fleet","text":"@operator the deploy is wedged"}`+"\n"+
		"ok\nok\nok\n")
	hub, tg := startTelegramFleetIn(t, dataDir, operator, map[string]any{"workspace_path": workspace})
	hub.waitCompleted("alpha", 1)
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, hub, "alpha")
	hub.waitCompleted("alpha", 2)
	hub.mustJSON("PUT", "/api/settings", map[string]any{"health_send_grace_minutes": 1}, nil)

	tg.failSendsContaining("the deploy is wedged")
	hub.message("alpha", "say it")
	if !hub.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	within(t, 120*time.Second, "alpha's send_failed problem", func() bool {
		return hub.loop("alpha").Health == "warning"
	})
	tg.waitSentFrom(t, operator.ID, "alpha", "a message alpha sent at")

	hub.mustJSON("POST", "/api/loops/alpha/problems/send_failed/acknowledge", nil, nil)
	tg.waitSentFrom(t, operator.ID, "alpha", "You will hear of them again at")
	hub.stop()

	hub = startTelegramServer(t, dataDir, tg)
	// past the grace an acknowledgement used to start over, and past the
	// restart that used to forget it
	time.Sleep(75 * time.Second)
	if view := hub.loop("alpha"); view.Health != "ok" || len(view.Problems) != 0 {
		t.Fatalf("health %q, problems %+v during the silence, want none", view.Health, view.Problems)
	}
	if told := countSent(tg, operator.ID, "a message alpha sent at"); told != 1 {
		t.Fatalf("the owner was told of the acknowledged send %d times, want once", told)
	}
}
