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
