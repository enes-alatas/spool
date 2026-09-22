//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A surface send used to get one attempt: a timeout against api.telegram.org
// logged a warning and the message was gone — not retried, not marked, and
// reported to the loop as sent. A network blip cost this fleet five messages
// across three loops in two minutes, including a merge ask nobody knew had
// not been asked (#147).

// TestSendSurvivesABlip: a send refused twice gets through on a later
// attempt, and the message carries no failure afterwards.
func TestSendSurvivesABlip(t *testing.T) {
	operator := user{ID: 7711, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta worth saying twice"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	before := tg.sendAttempts()
	tg.failNextSends(2)
	srv.message("alpha", "say it")

	tg.waitSent(t, groupChatID, "worth saying twice")
	if got := tg.sendAttempts() - before; got < 3 {
		t.Fatalf("the send took %d attempts; two were refused, so it cannot have arrived in fewer than 3", got)
	}
	stored := srv.activityWith("@beta worth saying twice")
	if len(stored) != 1 {
		t.Fatalf("message stored %d times: %s", len(stored), dump(stored))
	}
	if stored[0].SendFailedAt != 0 || stored[0].SendError != "" {
		t.Fatalf("a message that arrived still carries a failure: %s", dump(stored[0]))
	}
}

// TestSendThatNeverGetsThroughIsRecorded: when the retries run out, the
// failure is on the message and in the loop's timeline. The operator can see
// that words meant for them never arrived, which is the whole point — the
// loop's own view of the world already says it spoke.
func TestSendThatNeverGetsThroughIsRecorded(t *testing.T) {
	operator := user{ID: 7722, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta into the void"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1) // every send from here on
	srv.message("alpha", "say it")

	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("a send that never arrived left nothing on the loop's timeline")
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		stored := srv.activityWith("@beta into the void")
		if len(stored) == 1 && stored[0].SendFailedAt > 0 {
			if !strings.Contains(stored[0].SendError, "502") {
				t.Fatalf("the message records %q, which does not say what went wrong", stored[0].SendError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message was never marked undelivered: %s", dump(stored))
		}
		time.Sleep(500 * time.Millisecond)
	}

	// and the surface itself never saw it — the failure is real, not cosmetic
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "into the void") {
			t.Fatal("the stand-in recorded a message it refused")
		}
	}
}

// TestALostSendIsToldToItsSenderAtTheNextWake: the loop that said something
// nobody read finds out. The outcome of a send lands after the turn that
// made it has ended, so the next wake is the first moment the sender can be
// told — and it is told ahead of that wake's own envelopes, so it knows what
// it failed to say before it decides what to say next (#154).
func TestALostSendIsToldToItsSenderAtTheNextWake(t *testing.T) {
	operator := user{ID: 7733, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1) // every send from here on
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	srv.waitState("alpha", "asleep", 60*time.Second)

	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	next := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "anything new")
	})
	// The prefix, not a substring: the news leads the turn.
	if !strings.HasPrefix(next.ResultText, "echo: [system note · 1 of your message never arrived") {
		t.Fatalf("the turn the loop received does not open with the news:\n%s", next.ResultText)
	}
	for _, want := range []string{
		"to group:",                  // where it was going, in send_message's words
		"502",                        // and why, as the surface said it
		"@beta the deploy is wedged", // enough of it to know which message
		"anything new",               // and the wake's own envelope, after all of it
	} {
		if !strings.Contains(next.ResultText, want) {
			t.Fatalf("the news lacks %q:\n%s", want, next.ResultText)
		}
	}

	// And it is said once: a loop told twice about the same lost message
	// would resend it twice, or distrust the news.
	srv.waitState("alpha", "asleep", 60*time.Second)
	again := time.Now().UnixMilli()
	srv.message("alpha", "and now")
	third := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= again && strings.Contains(tn.ResultText, "and now")
	})
	if strings.Contains(third.ResultText, "never arrived") {
		t.Fatalf("the loop was told about the same lost message twice:\n%s", third.ResultText)
	}
}

// TestALostSendIsNotSpentOnAHandoffTurn: a rotation's handoff turn is the
// one turn that cannot act on this news — that session is ending, and its
// reply is a note to its successor. Spending the news there would announce a
// lost message to the one session that can do nothing about it, and mark it
// told. It is deferred instead: nothing is marked until a turn completes, so
// the successor's first turn is where the loop hears it (#154, ADR-0024).
func TestALostSendIsNotSpentOnAHandoffTurn(t *testing.T) {
	operator := user{ID: 7744, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta said into a dead line"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1) // every send from here on
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	srv.waitState("alpha", "asleep", 60*time.Second)

	// The loop now owes itself news, and rotates before it can be told.
	srv.mustJSON("POST", "/api/loops/alpha/rotate", nil, nil)
	at := time.Now().UnixMilli()
	srv.message("alpha", "after the rotation")

	handoff := srv.waitTurn("alpha", 60*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && tn.Trigger == "rotation"
	})
	if strings.Contains(handoff.ResultText, "never arrived") {
		t.Fatalf("the news was spent on the handoff turn, which cannot act on it:\n%s", handoff.ResultText)
	}

	successor := srv.waitTurn("alpha", 60*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "after the rotation")
	})
	if successor.SessionID == handoff.SessionID {
		t.Fatalf("the loop was expected to rotate onto a fresh session, still on %s", handoff.SessionID)
	}
	if !strings.Contains(successor.ResultText, "never arrived") ||
		!strings.Contains(successor.ResultText, "said into a dead line") {
		t.Fatalf("the successor was never told what its predecessor failed to say:\n%s", successor.ResultText)
	}
}

// TestUndeliveredCountReachesTheFleetView: the operator who is not reading a
// loop's timeline still learns its words are not arriving. The count is on
// the loop's own view, and it counts the sender's failures — the loop that
// was merely mentioned is healthy and says so (#202).
func TestUndeliveredCountReachesTheFleetView(t *testing.T) {
	operator := user{ID: 7733, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta nobody hears this"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("a loop that has not sent anything reports %d undelivered", n)
	}

	tg.failNextSends(-1) // every send from here on
	srv.message("alpha", "say it")

	deadline := time.Now().Add(30 * time.Second)
	for {
		n := srv.loop("alpha").Undelivered
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sender's view reports %d undelivered after a send that never arrived", n)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// the recipient is not the sender: beta's own messages all arrived
	if n := srv.loop("beta").Undelivered; n != 0 {
		t.Fatalf("beta reports %d undelivered, but the failed message was alpha's", n)
	}
}
