//go:build integration

package itest

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A loop's send that stays undelivered past the grace is a problem: the
// owner hears of it once, and the loop is woken once, its turn opening
// with the note about the lost send. Its resend lands, which resolves the
// failure and clears the problem, and the owner hears of that once too
// (#1144).
func TestAnUndeliveredSendIsToldToTheOwnerAndWakesTheLoopOnce(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7878, First: "Operator", Username: "operator"}
	workspace := workspaceWithScript(t, "ok\nok\n"+
		`!send {"destination":"group","text":"@operator the deploy is wedged"}`+"\n"+
		`!send {"destination":"group","text":"@operator repeating what failed","resends":"$ref"}`+"\n"+
		"!echo\n")
	hub, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspace})
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
	tg.failSendsContaining("")
	hub.waitCompleted("alpha", 3)
	if view := hub.loop("alpha"); len(view.Problems) != 0 {
		t.Fatalf("problems = %+v within the grace, want none", view.Problems)
	}

	within(t, 120*time.Second, "alpha's send_failed problem", func() bool {
		return hub.loop("alpha").Health == "warning"
	})
	tg.waitSentFrom(t, operator.ID, "alpha", "a message alpha sent at")
	hub.waitCompleted("alpha", 4)
	tg.waitSent(t, groupChatID, "repeating what failed")
	within(t, 30*time.Second, "the resend clearing alpha's send_failed problem", func() bool {
		return hub.loop("alpha").Health == "ok"
	})
	tg.waitSentFrom(t, operator.ID, "alpha", "alpha has no undelivered messages left")
	time.Sleep(2 * time.Second)
	if told := countSent(tg, operator.ID, "a message alpha sent at"); told != 1 {
		t.Fatalf("the owner was told of one undelivered send %d times, want once", told)
	}
	if done := len(hub.completed("alpha")); done != 4 {
		t.Fatalf("alpha finished %d turns, want 4: the problem woke it more than once", done)
	}
}

// Failed sends from three loops on one surface within five minutes say the
// surface is down. Each loop's problem opens and shows, but no loop is
// woken for it and no owner is told, until a send on that surface gets
// through: the operator's retry of gamma's message here. Then alpha and
// beta are each woken once and alpha's owner told once; gamma's only
// failure was the one the retry resolved, so it clears without a word or a
// wake (#1144).
func TestASurfaceOutageHoldsTheWakesUntilASendGetsThrough(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7979, First: "Operator", Username: "operator"}
	tg := startFakeTelegram(t, "alpha", "beta", "gamma")
	hub := startTelegramServer(t, t.TempDir(), tg)
	names := []string{"alpha", "beta", "gamma"}
	for _, name := range names {
		first := "ok\n"
		if name == "alpha" {
			first = "ok\nok\n" // and its owner's first DM
		}
		workspace := workspaceWithScript(t, first+
			`!send {"destination":"group","text":"@operator lost in the outage, from `+name+`"}`+"\n!echo\n")
		hub.createLoop(name, map[string]any{"tg_bot_token": name, "workspace_path": workspace})
	}
	hub.allowOwner(tg, "alpha", operator)
	tg.post(groupChatID, "supergroup", "binding", operator)
	settleBindings()
	for _, name := range names {
		hub.waitCompleted(name, 1)
	}
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, hub, "alpha")
	hub.waitCompleted("alpha", 2)
	// alpha has taken one turn more than the others, for its owner's DM
	base := map[string]int{"alpha": 1, "beta": 0, "gamma": 0}
	hub.mustJSON("PUT", "/api/settings", map[string]any{"health_send_grace_minutes": 1}, nil)

	tg.failSendsContaining("lost in the outage")
	for _, name := range names {
		hub.message(name, "say it")
	}
	lost := hub.waitUndelivered(3, 90*time.Second)
	for _, name := range names {
		hub.waitCompleted(name, base[name]+2)
	}
	within(t, 120*time.Second, "every loop's send_failed problem", func() bool {
		for _, name := range names {
			if view := hub.loop(name); len(view.Problems) != 1 || view.Problems[0].Symptom != "send_failed" {
				return false
			}
		}
		return true
	})
	time.Sleep(2 * time.Second)
	if told := countSent(tg, operator.ID, "a message alpha sent at"); told != 0 {
		t.Fatalf("the owner was told %d times while the surface was down, want none", told)
	}
	for _, name := range names {
		if done := len(hub.completed(name)); done != base[name]+2 {
			t.Fatalf("%s finished %d turns while the surface was down, want %d: no wake", name, done, base[name]+2)
		}
	}

	tg.failSendsContaining("")
	gamma := hub.loop("gamma").ID
	var retried int64
	for _, row := range lost {
		if row.FromLoopID == gamma {
			retried = row.ID
		}
	}
	if retried == 0 {
		t.Fatalf("no undelivered row from gamma in %s", dump(lost))
	}
	hub.mustJSON("POST", "/api/messages/"+strconv.FormatInt(retried, 10)+"/retry", nil, nil)

	tg.waitSentFrom(t, operator.ID, "alpha", "a message alpha sent at")
	hub.waitCompleted("alpha", 4)
	hub.waitCompleted("beta", 3)
	hub.waitHealth("gamma", "ok")
	time.Sleep(2 * time.Second)
	for name, want := range map[string]int{"alpha": 4, "beta": 3, "gamma": 2} {
		if done := len(hub.completed(name)); done != want {
			t.Errorf("%s finished %d turns after the surface came back, want %d", name, done, want)
		}
	}
	if told := countSent(tg, operator.ID, "a message alpha sent at"); told != 1 {
		t.Fatalf("the owner was told %d times after the surface came back, want once", told)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(fmt.Sprint(hub.loop(name).Problems), "send_failed") {
			t.Errorf("%s's problem cleared with its send still undelivered", name)
		}
	}
}
