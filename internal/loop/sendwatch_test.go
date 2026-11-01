package loop

import (
	"context"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
)

// Failed sends from three loops on one surface within five minutes mean
// the surface is down; two loops, or failures spread wider apart, do not.
// A send it carries ends the outage, and only for that surface (#1144).
func TestSendOutagesTakeThreeLoopsWithinFiveMinutes(t *testing.T) {
	outages := newSendOutages()
	minute := time.Minute.Milliseconds()
	fail := func(surfaceName, loopID string, at int64) (bool, bool) {
		return outages.note(&surface.SendOutcome{Surface: surfaceName, LoopID: loopID, Failed: true}, at)
	}
	if started, _ := fail("telegram", "a", 0); started {
		t.Fatal("one loop's failure started an outage")
	}
	fail("telegram", "a", minute) // the same loop again counts once
	if started, _ := fail("slack", "b", minute); started {
		t.Fatal("failures on two surfaces were counted as one")
	}
	if started, _ := fail("telegram", "c", 6*minute); started {
		t.Fatal("a failure five minutes after the others started an outage")
	}
	if outages.holds("a") {
		t.Fatal("a surface that is up holds a loop")
	}
	if started, _ := fail("telegram", "d", 7*minute); started {
		t.Fatal("two loops within five minutes started an outage")
	}
	// a send that gets through before the outage starts leaves the count be,
	// or a flapping surface would never trip the guard
	if _, ended := outages.note(&surface.SendOutcome{Surface: "telegram", LoopID: "x"}, 7*minute); ended {
		t.Fatal("a send on a surface that was up ended an outage")
	}
	if started, _ := fail("telegram", "e", 8*minute); !started {
		t.Fatal("three loops within five minutes did not start an outage")
	}
	if !outages.holds("c") || !outages.holds("e") || outages.holds("b") {
		t.Fatal("the outage holds the wrong loops")
	}
	if _, ended := outages.note(&surface.SendOutcome{Surface: "slack"}, 9*minute); ended {
		t.Fatal("a send on another surface ended the outage")
	}
	if _, ended := outages.note(&surface.SendOutcome{LoopID: "c"}, 9*minute); ended {
		t.Fatal("a dismissal ended the outage")
	}
	if _, ended := outages.note(&surface.SendOutcome{Surface: "telegram", LoopID: "x"}, 9*minute); !ended {
		t.Fatal("a send telegram carried did not end its outage")
	}
	if outages.holds("c") {
		t.Fatal("the loop is still held once its surface is up")
	}
}

// sendWatchActor is a loop "gardener" whose send failed two minutes ago,
// with a grace of a minute; its actor runs no goroutine and is busy, so a
// wake queues rather than spawning (#1144).
func sendWatchActor(t *testing.T) (*Actor, store.Store, *store.Message, <-chan bus.Item) {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	loopRecord := store.Loop{
		ID: "l1", Name: "gardener", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := st.Loops().Create(ctx, &loopRecord); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().Set(ctx, store.SettingHealthSendGraceMinutes, "1"); err != nil {
		t.Fatal(err)
	}
	failedAt := now() - 2*time.Minute.Milliseconds()
	lost := &store.Message{TS: failedAt, Origin: store.OriginLoop, Author: "gardener", FromLoopID: "l1",
		Text: "the roses are in", Conversation: store.ConversationGroup}
	if err := st.Messages().Insert(ctx, lost); err != nil {
		t.Fatal(err)
	}
	if err := st.Messages().SetSendResult(ctx, lost.ID, failedAt, "chat not found"); err != nil {
		t.Fatal(err)
	}
	lost.SendFailedAt = failedAt
	hub := bus.New()
	notices, cancel := hub.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindLoopProblem })
	t.Cleanup(cancel)
	actor := &Actor{deps: Deps{Store: st, Bus: hub}, loop: loopRecord, state: StateBusy, sendTimer: time.NewTimer(time.Hour)}
	actor.sendTimer.Stop()
	actor.loadProblems()
	return actor, st, lost, notices
}

func wakes(actor *Actor) int {
	woken := 0
	for _, env := range actor.inbox {
		if env.wakeOnly() {
			woken++
		}
	}
	return woken
}

// A send undelivered past the grace opens send_failed once, dated from the
// failure, and wakes the loop once for it. Resolving the send clears the
// problem, and the owner hears of both (#1144).
func TestAnUndeliveredSendPastTheGraceOpensSendFailed(t *testing.T) {
	actor, st, lost, notices := sendWatchActor(t)

	actor.watchSends()
	notice := nextNotice(t, notices)
	if !notice.Opened || notice.Symptom != store.SymptomSendFailed || notice.Since != lost.SendFailedAt {
		t.Fatalf("told %+v, want send_failed opened since %d", notice, lost.SendFailedAt)
	}
	if woken := wakes(actor); woken != 1 {
		t.Fatalf("the problem woke the loop %d times, want once", woken)
	}
	actor.watchSends()
	noNotice(t, notices, "the problem was open already")
	if woken := wakes(actor); woken != 1 {
		t.Fatalf("a second look woke the loop %d times in all, want once", woken)
	}

	if _, err := st.Messages().ResolveSend(context.Background(), lost.ID, now(), store.SendResolutionDismissed, 0); err != nil {
		t.Fatal(err)
	}
	actor.watchSends()
	if notice := nextNotice(t, notices); notice.Opened || notice.ByOperator {
		t.Fatalf("the resolved send told %+v, want a recovery", notice)
	}
}

// A send within its grace opens nothing, and the setting at 0 opens
// nothing past it (#1144).
func TestASendWithinItsGraceOpensNothing(t *testing.T) {
	actor, st, _, notices := sendWatchActor(t)
	if err := st.Settings().Set(context.Background(), store.SettingHealthSendGraceMinutes, "5"); err != nil {
		t.Fatal(err)
	}
	actor.watchSends()
	if err := st.Settings().Set(context.Background(), store.SettingHealthSendGraceMinutes, "0"); err != nil {
		t.Fatal(err)
	}
	actor.watchSends()
	noNotice(t, notices, "the send was within its grace, or the symptom off")
	if open, woken := actor.Problems(), wakes(actor); len(open) != 0 || woken != 0 {
		t.Fatalf("open problems %+v and %d wakes, want none", open, woken)
	}
}

// While the surface looks down, the problem opens and shows, but neither
// the owner nor the loop hears of it; once the surface is up, both do,
// once. A problem resolved before then clears without a word (#1144).
func TestAnOutageHoldsTheNoticeAndTheWake(t *testing.T) {
	for _, resolveFirst := range []bool{false, true} {
		actor, st, lost, notices := sendWatchActor(t)
		down := true
		actor.deps.SendOutage = func(string) bool { return down }

		actor.watchSends()
		noNotice(t, notices, "the surface is down")
		if !actor.hasProblem(store.SymptomSendFailed) {
			t.Fatalf("open problems = %+v, want send_failed showing", actor.Problems())
		}
		if woken := wakes(actor); woken != 0 {
			t.Fatalf("the loop was woken %d times while its surface is down, want none", woken)
		}

		if resolveFirst {
			if _, err := st.Messages().ResolveSend(context.Background(), lost.ID, now(), store.SendResolutionDismissed, 0); err != nil {
				t.Fatal(err)
			}
			actor.watchSends()
			noNotice(t, notices, "the held problem cleared")
			if open := actor.Problems(); len(open) != 0 {
				t.Fatalf("open problems = %+v, want none", open)
			}
			continue
		}
		down = false
		actor.watchSends()
		if notice := nextNotice(t, notices); !notice.Opened || notice.Since != lost.SendFailedAt {
			t.Fatalf("the surface coming back told %+v, want the held opening", notice)
		}
		if woken := wakes(actor); woken != 1 {
			t.Fatalf("the surface coming back woke the loop %d times, want once", woken)
		}
		actor.watchSends()
		noNotice(t, notices, "the held notice went out already")
	}
}
