//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A loop told its words never arrived can decide they are no longer worth
// saying, and say so (#561). Before this, the only way a loop could deal
// with a lost send was to say it again; one it let go stayed on the
// operator's list for them to dismiss by hand.
//
// The dismissal names the destination the message was lost going to, as a
// resend does, so the turn tries the wrong one first: that is refused by
// name and resolves nothing, and the right one resolves the failure.
func TestALoopDismissesItsOwnLostSend(t *testing.T) {
	t.Parallel()
	operator := user{ID: 7784, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta the deploy is wedged"}`+"\n"+
		`!send {"destination":"control_room","dismiss":"$ref"} !send {"destination":"group","dismiss":"$ref"}`+"\n"+
		"!echo\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.failNextSends(-1)
	srv.message("alpha", "say it")
	if !srv.hasEvent("alpha", "send_failed", 60*time.Second) {
		t.Fatal("the send was expected to be given up on")
	}
	lost := srv.waitUndelivered(1, 30*time.Second)[0]
	srv.waitState("alpha", "asleep", 60*time.Second)

	// The next turn opens with the note, so "$ref" is the lost message's
	// reference, which is what a loop reading the note would pass.
	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	dismissing := srv.waitTurn("alpha", 60*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "dismiss")
	})
	if !strings.Contains(dismissing.ResultText, "dismiss_wrong_destination") {
		t.Fatalf("the dismissal to the wrong destination was not refused by name:\n%s", dismissing.ResultText)
	}
	if strings.Count(dismissing.ResultText, "send error") != 1 {
		t.Fatalf("the dismissal to the destination it was lost going to was refused too:\n%s", dismissing.ResultText)
	}

	if got := srv.waitUndelivered(0, 30*time.Second); len(got) != 0 {
		t.Fatalf("the loop's dismissal did not take the failure off the operator's list: %s", dump(got))
	}
	if n := srv.loop("alpha").Undelivered; n != 0 {
		t.Fatalf("the badge still counts %d after the loop dismissed the failure", n)
	}
	var resolved activityMessage
	for _, m := range srv.activity() {
		if m.ID == lost.ID {
			resolved = m
		}
	}
	if resolved.SendResolution != "dismissed_by_loop" {
		t.Errorf("the failure resolved as %q, want dismissed_by_loop", resolved.SendResolution)
	}
	if resolved.SendFailedAt == 0 || resolved.SendError == "" {
		t.Error("the dismissed row lost the failure it is a record of")
	}
	// Nothing was said: a dismissal sends no message anywhere.
	for _, m := range srv.activity() {
		if m.ID > lost.ID && m.Origin == "loop" {
			t.Fatalf("the dismissal stored a message: %s", dump(m))
		}
	}

	// And the loop is not reminded of what it dismissed.
	again := time.Now().UnixMilli()
	srv.message("alpha", "and now")
	next := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= again && strings.Contains(tn.ResultText, "and now")
	})
	if strings.Contains(next.ResultText, "never arrived") {
		t.Fatalf("the loop was reminded of a failure it dismissed:\n%s", next.ResultText)
	}
}

// The refusals reachable without an outage, in one place: each resolves
// nothing, so a loop cannot close a failure that is not its own to close, or
// one that is not a failure.
func TestDismissRefusesWhatItCannotResolve(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	briar := mcpSession(t, s, hubMCPToken(t, s, "briar"))

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
			"destination": "control_room", "dismiss": "the lost one"}, "dismiss_not_failed"},
		{"a reference to nothing", map[string]any{
			"destination": "control_room", "dismiss": "ref:999999"}, "dismiss_not_failed"},
		{"a message that never failed", map[string]any{
			"destination": "control_room", "dismiss": mine}, "dismiss_not_failed"},
		{"another loop's message", map[string]any{
			"destination": "control_room", "dismiss": theirs}, "dismiss_not_failed"},
		{"words with it", map[string]any{
			"destination": "control_room", "dismiss": mine, "text": "never mind"}, "dismiss_carries_nothing_else"},
		{"a reaction with it", map[string]any{
			"destination": "control_room", "dismiss": mine, "react": "👍", "reply_to": mine}, "one_kind_of_send"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantSendError(t, callSend(t, aster, c.args), c.code)
		})
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "never mind") {
			t.Fatalf("a refused dismissal stored its words: %s", dump(m))
		}
	}
}
