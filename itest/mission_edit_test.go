//go:build integration

package itest

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// patchResult is the PATCH response read for the one field the loop row does
// not have: what the save did about the loop's session.
type patchResult struct {
	Mission  string `json:"mission"`
	Rotation string `json:"rotation"`
}

// A mission is part of the loop's prompt, and a running session cannot be
// given a new one (#162) — so a saved mission is not in force until the loop
// rotates. Saving asks for that rotation (#261), and the proof is the prompt
// the successor session runs: the new mission in it, the old one gone.
func TestSavingAMissionRotatesTheSession(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("scribe", map[string]any{
		"workspace_path": ws, "workspace_mode": "dir", "mission": "Keep the ledger."})

	first := waitPrompt(t, s, "scribe", 0)
	if !strings.Contains(first.ResultText, "Keep the ledger.") {
		t.Fatalf("the loop did not start under the mission it was created with:\n%s", first.ResultText)
	}
	s.waitState("scribe", "asleep", 30*time.Second)

	var saved patchResult
	s.mustJSON("PATCH", "/api/loops/scribe", map[string]any{"mission": "Read the ledger."}, &saved)
	if saved.Mission != "Read the ledger." {
		t.Fatalf("mission = %q, want the edit to have landed", saved.Mission)
	}
	if saved.Rotation != "queued" {
		t.Fatalf("rotation = %q, want %q: the loop has a session and the mission changed", saved.Rotation, "queued")
	}
	at := time.Now().UnixMilli()

	// The rotation was asked for by the save alone — no client rotate call
	// follows — and it is a fresh session that carries the new mission,
	// because a resumed one keeps the prompt it was created with.
	s.message("scribe", "after the save")
	next := s.waitTurn("scribe", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && tn.SessionID != first.SessionID &&
			strings.Contains(tn.ResultText, "WHO YOU CAN ADDRESS")
	})
	if !strings.Contains(next.ResultText, "Read the ledger.") {
		t.Fatalf("the session the rotation started does not run the saved mission:\n%s", next.ResultText)
	}
	if strings.Contains(next.ResultText, "Keep the ledger.") {
		t.Fatalf("the replaced mission survived into the new session's prompt:\n%s", next.ResultText)
	}
}

// The other half, and the one an operator pays for if it is wrong: a
// rotation costs the loop every turn of context it has, so a Save on text
// nobody changed must not buy one.
func TestSavingAnUnchangedMissionDoesNotRotate(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("keeper", map[string]any{
		"workspace_path": ws, "workspace_mode": "dir", "mission": "Keep the ledger."})

	first := waitPrompt(t, s, "keeper", 0)
	s.waitState("keeper", "asleep", 30*time.Second)

	// Trailing whitespace and all: what a textarea collects is not an edit.
	var saved patchResult
	s.mustJSON("PATCH", "/api/loops/keeper", map[string]any{"mission": "Keep the ledger.\n"}, &saved)
	if saved.Rotation != "none" {
		t.Fatalf("rotation = %q, want %q: nothing about the mission changed", saved.Rotation, "none")
	}
	at := time.Now().UnixMilli()

	s.message("keeper", "after the save")
	next := s.waitTurn("keeper", 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "WHO YOU CAN ADDRESS")
	})
	if next.SessionID != first.SessionID {
		t.Fatalf("the loop rotated on a save that changed nothing: %s -> %s", first.SessionID, next.SessionID)
	}
}

// A loop with no mission is a loop with no instructions, and the column
// would take one: the create path refuses an empty mission, and so does this.
func TestPatchRefusesAnEmptyMission(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("warden", map[string]any{"mission": "Keep the ledger."})

	for _, empty := range []string{"", "   \n "} {
		resp, body := s.do("PATCH", "/api/loops/warden", map[string]any{"mission": empty})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("PATCH mission %q: status = %d, want 400 (%s)", empty, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "mission is required") {
			t.Fatalf("PATCH mission %q: refusal does not say why: %s", empty, body)
		}
	}
	if got := s.loop("warden").Mission; got != "Keep the ledger." {
		t.Fatalf("mission = %q, want the refused saves to have written nothing", got)
	}
}
