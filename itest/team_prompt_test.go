//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A loop in a team is told its team's name and description under WHO YOU
// CAN ADDRESS, above the team's channels; a loop in no team is shown the
// prompt it had before any team existed; and a renamed team reaches its
// loops at their next wake (ADR-0066).
func TestThePromptNamesTheLoopsTeam(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.createLoop("aster", map[string]any{"workspace_path": workspaceWithScript(t, "!echo\n"), "workspace_mode": "dir"})
	hub.createLoop("briar", map[string]any{"workspace_path": workspaceWithScript(t, "!sysprompt\n"), "workspace_mode": "dir"})

	at := time.Now().UnixMilli()
	hub.message("briar", "who is there")
	before := waitPrompt(t, hub, "briar", at)
	// aster's session starts before any team, so its wake below carries the
	// change as a standing-instructions note
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	hub.waitState("aster", "asleep", 30*time.Second)

	// ops sorts before red, so red listed first is the team's doing
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "ops"}, nil)
	hub.wantRefusal("PUT", "/api/channels/ops/loops/aster", nil, 204, "")
	hub.mustJSON("POST", "/api/teams", map[string]any{"name": "red", "description": "Builds the hub."}, nil)
	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"team": "red"}, nil)

	at = time.Now().UnixMilli()
	hub.message("aster", "who is there")
	note := hub.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "who is there")
	}).ResultText
	want := "- You are in the team red — Builds the hub.\n" +
		"  Its channels come first below, and only its loops can be in them.\n" +
		"- Your other channels, each its own conversation:"
	if !strings.Contains(note, want) {
		t.Fatalf("aster's wake lacks %q:\n%s", want, note)
	}
	if red, ops := strings.Index(note, "    channel:red"), strings.Index(note, "    channel:ops"); red < 0 || ops < red {
		t.Errorf("red's home channel is not listed above the hub channel:\n%s", note)
	}

	// briar, in no team, is shown at a fresh session what it was before
	after := waitPromptAfterRotation(t, hub, "briar", before.SessionID)
	if after != before.ResultText {
		t.Errorf("a loop in no team was shown a changed prompt:\nbefore:\n%s\nafter:\n%s", before.ResultText, after)
	}

	hub.waitState("aster", "asleep", 30*time.Second)
	hub.mustJSON("PATCH", "/api/teams/red", map[string]any{"name": "blue"}, nil)
	at = time.Now().UnixMilli()
	hub.message("aster", "and now")
	next := hub.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "and now")
	})
	if !strings.Contains(next.ResultText, "- You are in the team blue — Builds the hub.") {
		t.Errorf("the wake after red was renamed blue does not name blue:\n%s", next.ResultText)
	}
}
