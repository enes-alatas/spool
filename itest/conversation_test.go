//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// TestSeparateTurnsPerConversation: a control-room message and a group
// message queued behind a busy turn must not blend into one answer — the
// actor runs one turn per conversation (ADR-0026).
func TestSeparateTurnsPerConversation(t *testing.T) {
	// alpha's first turn hangs long enough for both arrivals to queue;
	// afterwards it echoes (a bare !ctx keeps the echo reply), so each
	// turn's reply names its own inputs.
	wsAlpha := workspaceWithScript(t, "!hang 4\n!ctx 0\n")
	wsBeta := workspaceWithScript(t, `!send {"destination":"group","text":"@alpha group ping"}`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("alpha", map[string]any{"workspace_path": wsAlpha})
	s.createLoop("beta", map[string]any{"workspace_path": wsBeta})

	s.message("alpha", "start hanging")
	time.Sleep(300 * time.Millisecond) // alpha is inside the hang
	s.message("alpha", "web note")     // control_room conversation
	s.message("beta", "go")            // beta's turn group-sends to alpha

	webTurn := s.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "web note")
	})
	groupTurn := s.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "group ping")
	})
	if webTurn.ID == groupTurn.ID {
		t.Fatalf("control_room and group inputs shared one turn: %s", dump(webTurn))
	}
	if strings.Contains(webTurn.ResultText, "group ping") || strings.Contains(groupTurn.ResultText, "web note") {
		t.Fatalf("conversation inputs blended across turns:\nweb: %s\ngroup: %s",
			webTurn.ResultText, groupTurn.ResultText)
	}
	// the echoed input carries the envelope header: the model must see which
	// conversation each message belongs to (ADR-0026)
	if !strings.Contains(webTurn.ResultText, "· control_room") {
		t.Fatalf("control_room input not labeled for the model: %s", webTurn.ResultText)
	}
	if !strings.Contains(groupTurn.ResultText, "· group") {
		t.Fatalf("group input not labeled for the model: %s", groupTurn.ResultText)
	}
}

// TestControlRoomThreadEndpoint: the per-loop conversation endpoint returns
// exactly the private thread — the operator's composer messages and the
// loop's control_room sends, newest first — and refuses the group kind,
// which has no per-loop thread.
func TestControlRoomThreadEndpoint(t *testing.T) {
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"control_room","text":"thread reply"}`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})

	s.message("aster", "thread question")

	var thread []activityMessage
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.mustJSON("GET", "/api/loops/aster/conversation", nil, &thread)
		if len(thread) >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(thread) != 2 || thread[0].Text != "thread reply" || thread[1].Text != "thread question" {
		t.Fatalf("control_room thread = %s, want the reply then the question", dump(thread))
	}
	for _, m := range thread {
		if m.Conversation != "control_room" {
			t.Fatalf("thread returned a %q message: %s", m.Conversation, dump(m))
		}
	}

	if resp, _ := s.do("GET", "/api/loops/aster/conversation?conversation=group", nil); resp.StatusCode != 400 {
		t.Fatalf("group thread request answered %d, want 400", resp.StatusCode)
	}
}
