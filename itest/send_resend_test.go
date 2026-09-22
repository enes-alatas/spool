//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A loop told its words never arrived says them again, and the failure it
// names resolves when the new send lands (#270). Before this, the resend was
// an unrelated message: the operator had to dismiss by hand a failure the
// loop had already dealt with, which is the hub asking a human to finish a
// job that finished itself.
//
// The reference the loop passes is the one the undelivered note gave it —
// the script sends "$ref", which the stand-in fills from the turn it is
// answering, so the ref travels the same path a real loop's would.
func TestALoopsResendResolvesTheFailureItNames(t *testing.T) {
	operator := user{ID: 7781, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged (saying it again)","resends":"$ref"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]
	srv.waitState("alpha", "asleep", 60*time.Second)

	// Telegram is back, and the loop's next wake opens with the note naming
	// that message — so "$ref" in the script is the lost message's own
	// reference, which is what a loop reading the note would pass.
	tg.failNextSends(0)
	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	turn := srv.waitTurn("alpha", 60*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "sent")
	})
	if strings.Contains(turn.ResultText, "send error") {
		t.Fatalf("the resend was refused:\n%s", turn.ResultText)
	}
	tg.waitSent(t, groupChatID, "saying it again")

	if got := srv.waitUndelivered(0, 30*time.Second); len(got) != 0 {
		t.Fatalf("the loop's own resend did not resolve the failure it named: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("the badge still counts %d after the loop resent the message", n)
	}

	// Resolved, and resolved as a resend: the row says what happened to it
	// and which message carried the words the second time, so an operator
	// reading the failure can read what was actually said.
	var resolved, again activityMessage
	for _, m := range srv.activity() {
		switch {
		case m.ID == lost.ID:
			resolved = m
		case strings.Contains(m.Text, "saying it again"):
			again = m
		}
	}
	if resolved.SendResolution != "resent" {
		t.Errorf("the failure resolved as %q, want resent", resolved.SendResolution)
	}
	if resolved.SendResentAs != again.ID || again.ID == 0 {
		t.Errorf("the failure names message %d as the resend, want %d", resolved.SendResentAs, again.ID)
	}
	if resolved.SendFailedAt == 0 || resolved.SendError == "" {
		t.Error("the resolved row lost the failure it is a record of")
	}
}

// The destination is part of what makes a resend a resend: words that arrive
// somewhere else did not replace the ones that were lost. The refusal is
// in-turn and the message is not sent, so the loop can correct the call
// rather than find out afterwards that it said something in the wrong room
// and closed a failure that is still the operator's.
func TestAResendToAnotherDestinationIsRefused(t *testing.T) {
	operator := user{ID: 7782, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		`!send {"destination":"control_room","text":"the deploy is wedged","resends":"$ref"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]
	srv.waitState("alpha", "asleep", 60*time.Second)

	tg.failNextSends(0)
	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	turn := srv.waitTurn("alpha", 60*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "send")
	})
	if !strings.Contains(turn.ResultText, "resends_wrong_destination") {
		t.Fatalf("the misdirected resend was not refused by name:\n%s", turn.ResultText)
	}

	// Not sent, and not resolved: the failure is still the operator's.
	for _, m := range srv.activity() {
		if m.Conversation == "control_room" && strings.Contains(m.Text, "the deploy is wedged") {
			t.Fatal("the refused resend was sent anyway")
		}
	}
	rows := srv.undelivered()
	if len(rows) != 1 || rows[0].ID != lost.ID {
		t.Fatalf("the refused resend changed the undelivered list: %s", dump(rows))
	}
	if n := srv.loop("alpha").Undelivered; n != 1 {
		t.Errorf("the badge counts %d after a refused resend, want 1", n)
	}
}

// The refusals reachable without an outage, in one place: every one leaves
// the message unsent, because a loop that learned of the mistake afterwards
// would already have said the words twice.
func TestResendsRefusesWhatItCannotResolve(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	briar := mcpSession(t, s, hubMCPToken(t, s, "briar"))

	// One message of each loop's that got through, so the two "nothing to
	// resolve" cases differ only in who sent it.
	if res := callSend(t, aster, map[string]any{
		"destination": "control_room", "text": "a note that arrived"}); res.IsError {
		t.Fatalf("control_room send refused: %s", resultText(res))
	}
	mine := messageRef(waitStored(t, s, "a note that arrived"))
	if res := callSend(t, briar, map[string]any{
		"destination": "control_room", "text": "briar's own note"}); res.IsError {
		t.Fatalf("control_room send refused: %s", resultText(res))
	}
	theirs := messageRef(waitStored(t, s, "briar's own note"))

	for _, c := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"not a reference", map[string]any{
			"destination": "control_room", "text": "again", "resends": "the lost one"}, "resends_not_failed"},
		{"a reference to nothing", map[string]any{
			"destination": "control_room", "text": "again", "resends": "ref:999999"}, "resends_not_failed"},
		{"a message that never failed", map[string]any{
			"destination": "control_room", "text": "again", "resends": mine}, "resends_not_failed"},
		{"another loop's message", map[string]any{
			"destination": "control_room", "text": "again", "resends": theirs}, "resends_not_failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantSendError(t, callSend(t, aster, c.args), c.code)
		})
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "again") {
			t.Fatalf("a refused resend was stored: %s", dump(m))
		}
	}
}

// The outage case, which is the normal one: a send fails because the surface
// is down, and the resend a wake later meets the same outage. The loop is
// told about the new failure and says the words a third time; when that one
// lands, both failures resolve.
//
// This is the path a claim carried only with the send could not serve. The
// first failure has been reported by then — a loop is told about a lost
// message exactly once — so nothing would ever name it to the loop again,
// and nothing but a human hand could take it off the operator's list, for
// words that did in the end arrive.
func TestAFailedResendIsResolvedByTheNextOne(t *testing.T) {
	operator := user{ID: 7783, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged (second time)","resends":"$ref"}`+"\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged (third time)","resends":"$ref"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// The first send, lost.
	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the first send was expected to be given up on")
	}
	first := srv.waitUndelivered(1, 30*time.Second)[0]
	srv.waitState("alpha", "asleep", 60*time.Second)

	// The resend, into the same outage: the notice names the first failure,
	// the loop says it again, and that send is lost too. Two failures now,
	// for one set of words.
	srv.message("alpha", "anything new")
	rows := srv.waitUndelivered(2, 60*time.Second)
	var second undeliveredRow
	for _, r := range rows {
		if r.ID != first.ID {
			second = r
		}
	}
	if !strings.Contains(second.Text, "second time") {
		t.Fatalf("the second row is not the resend: %s", dump(rows))
	}
	srv.waitState("alpha", "asleep", 60*time.Second)

	// The surface is back. This wake's notice names the second failure —
	// the first has been reported already and is never named again — so the
	// loop's third send claims the second, and the chain does the rest.
	tg.failNextSends(0)
	srv.message("alpha", "and now")
	tg.waitSent(t, groupChatID, "third time")

	if got := srv.waitUndelivered(0, 30*time.Second); len(got) != 0 {
		t.Fatalf("a failure was left behind by the chain: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("the badge still counts %d once the words arrived", n)
	}

	// Both resolved as resent, and both naming the send that got through
	// rather than the attempt in between: an operator reading either row
	// wants the words that arrived.
	var landed activityMessage
	byID := map[int64]activityMessage{}
	for _, m := range srv.activity() {
		byID[m.ID] = m
		if strings.Contains(m.Text, "third time") {
			landed = m
		}
	}
	if landed.ID == 0 {
		t.Fatal("the send that got through is not in activity")
	}
	for _, id := range []int64{first.ID, second.ID} {
		row := byID[id]
		if row.SendResolution != "resent" {
			t.Errorf("message %d resolved as %q, want resent", id, row.SendResolution)
		}
		if row.SendResentAs != landed.ID {
			t.Errorf("message %d names %d as the resend, want %d (the one that arrived)",
				id, row.SendResentAs, landed.ID)
		}
	}
}
