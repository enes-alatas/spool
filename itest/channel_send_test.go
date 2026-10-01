//go:build integration

package itest

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// A loop posts to a channel it is in by name, and only that channel's loops
// are reached: @all there wakes its members and nobody else, the message is
// kept under the channel's name and out of the fleet channel's timeline, and
// the envelope names the channel so the answer goes back to it (ADR-0038).
func TestALoopPostsToItsChannel(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "backend", "description": "Go core"}, nil)
	for _, name := range []string{"aster", "briar"} {
		s.wantRefusal("PUT", "/api/channels/backend/loops/"+name, nil, 204, "")
	}
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	res := callSend(t, sess, map[string]any{"destination": "channel:backend", "text": "@all hello backend"})
	if res.IsError {
		t.Fatalf("channel send refused: %s", resultText(res))
	}
	s.waitTurn("briar", 20*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.Contains(tn.ResultText, "hello backend")
	})
	var header string
	for _, inputs := range s.turnInputs("briar") {
		for _, input := range inputs {
			if strings.Contains(input, "hello backend") {
				header = input
			}
		}
	}
	if !strings.Contains(header, "[message from @aster (loop) · channel:backend · ref:") {
		t.Errorf("briar's envelope does not name the channel:\n%s", header)
	}

	var sent activityMessage
	for _, m := range s.activity() {
		if m.Author == "aster" && m.Text == "@all hello backend" {
			sent = m
		}
	}
	briar := s.loop("briar")
	if sent.Conversation != "group" || sent.Channel != "backend" || sent.Mirror != "not_mirrored" ||
		len(sent.DeliveredTo) != 1 || sent.DeliveredTo[0] != briar.ID {
		t.Fatalf("stored channel send = %s, want it in backend, kept on the hub, delivered to briar alone", dump(sent))
	}
	var fleet, backend []activityMessage
	s.mustJSON("GET", "/api/group", nil, &fleet)
	s.mustJSON("GET", "/api/channels/backend/messages", nil, &backend)
	for _, m := range fleet {
		if m.ID == sent.ID {
			t.Error("a channel's message is in the fleet channel's timeline")
		}
	}
	if len(backend) == 0 || backend[0].ID != sent.ID {
		t.Errorf("backend's timeline = %s, want the send newest", dump(backend))
	}

	// cedar is outside backend: @all there never reached it, and a mention
	// of it there reaches nobody, so the send names no recipient.
	for _, inputs := range s.turnInputs("cedar") {
		for _, input := range inputs {
			if strings.Contains(input, "hello backend") {
				t.Errorf("cedar, outside backend, was delivered its message:\n%s", input)
			}
		}
	}
	wantSendError(t, callSend(t, sess, map[string]any{"destination": "channel:backend", "text": "@cedar hi"}), "no_recipients")

	// A reply stays in the channel it answers.
	ref := "ref:" + strconv.FormatInt(sent.ID, 10)
	wantSendError(t, callSend(t, sess, map[string]any{"destination": "group", "text": "@briar re", "reply_to": ref}), "cross_conversation_reply_to")
	res = callSend(t, sess, map[string]any{"destination": "channel:backend", "text": "@briar and more", "reply_to": ref})
	if res.IsError {
		t.Errorf("a reply in the channel was refused: %s", resultText(res))
	}
}

// A loop can post only to the channels it is in, under the name its prompt
// teaches, and is told which it has when it gets one wrong.
func TestChannelSendRefusals(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	s.wantRefusal("PUT", "/api/channels/backend/loops/briar", nil, 204, "")
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	for _, c := range []struct{ destination, code string }{
		{"channel:backend", "no_such_destination"},
		{"channel:nowhere", "no_such_destination"},
		{"channel:group", "invalid_destination"},
		{"backend", "invalid_destination"},
	} {
		res := callSend(t, sess, map[string]any{"destination": c.destination, "text": "@briar hi"})
		wantSendError(t, res, c.code)
		if c.code == "no_such_destination" && !strings.Contains(resultText(res), "group, control_room") {
			t.Errorf("%s: refusal does not list aster's destinations: %s", c.destination, resultText(res))
		}
	}
	for _, m := range s.activity() {
		if m.Author == "aster" && m.Text == "@briar hi" {
			t.Errorf("a refused channel send was stored: %s", dump(m))
		}
	}
}

// A loop's prompt lists its channels, each with its description and the
// other loops in it, and teaches the destination that reaches them.
func TestPromptListsTheLoopsChannels(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})
	for _, name := range []string{"briar", "cedar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "backend", "description": "Go core"}, nil)
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "design"}, nil)
	for _, member := range []string{"backend/loops/aster", "backend/loops/briar", "design/loops/cedar"} {
		s.wantRefusal("PUT", "/api/channels/"+member, nil, 204, "")
	}

	// A session keeps the prompt it was created with, and aster's first
	// wake may predate its channels; a fresh session renders them.
	at := time.Now().UnixMilli()
	s.message("aster", "which channels")
	prompt := waitPromptAfterRotation(t, s, "aster", waitPrompt(t, s, "aster", at).SessionID)
	for _, want := range []string{
		"    channel:<name>\n",
		"    channel:backend — Go core\n      loops: @briar\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "channel:design") {
		t.Errorf("prompt lists a channel aster is not in:\n%s", prompt)
	}
}
