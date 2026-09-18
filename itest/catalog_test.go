//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// The identity catalog is resolved per wake (#45): what a loop is told it
// can address must match what the fleet currently is — a peer added, an
// owner configured, a private chat finally captured — without a restart.

func TestCatalogFollowsTheFleet(t *testing.T) {
	s := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "!sysprompt\n")
	s.createLoop("aster", map[string]any{"workspace_path": ws, "workspace_mode": "dir"})

	at := time.Now().UnixMilli()
	s.message("aster", "who is there")
	first := waitPrompt(t, s, "aster", at)
	for _, want := range []string{
		"WHO YOU CAN ADDRESS",
		"You are @aster",
		"No other loops are registered right now",
		"no owner configured, so owner_dm has nobody to reach",
	} {
		if !strings.Contains(first.ResultText, want) {
			t.Fatalf("a lone, ownerless loop's catalog lacks %q:\n%s", want, first.ResultText)
		}
	}

	// A peer added while the loop sleeps does not reach its system prompt on
	// the next wake: that wake resumes a session whose prompt was fixed when
	// it was created (#162). The loop is told in the turn instead — pinned
	// by TestStandingInstructionsReachARunningSession — and the prompt
	// itself catches up at the loop's next rotation.
	s.waitState("aster", "asleep", 30*time.Second)
	s.createLoop("briar", map[string]any{"mission": "review what aster writes"})
	at = time.Now().UnixMilli()

	s.message("aster", "and now")
	second := waitPrompt(t, s, "aster", at)
	if second.SessionID != first.SessionID {
		t.Fatalf("the loop was expected to resume its session, not rotate yet: %s -> %s", first.SessionID, second.SessionID)
	}
	if strings.Contains(second.ResultText, "@briar") {
		t.Fatalf("a resumed session was expected to still run the catalog it was created with:\n%s", second.ResultText)
	}

	third := waitPromptAfterRotation(t, s, "aster", first.SessionID)
	if !strings.Contains(third, "@briar — review what aster writes") {
		t.Fatalf("a peer added since the last wake never reached the prompt:\n%s", third)
	}
}

// With telegram configured, the catalog names the loop's own bot, its
// owner, and whether it can reach them privately yet — the part a loop
// cannot discover for itself, since owner_dm simply fails until the owner
// has written.
func TestCatalogNamesTheOwnerAndDMReadiness(t *testing.T) {
	operator := user{ID: 7171, First: "Operator", Username: "operator"}
	ws := workspaceWithScript(t, "!sysprompt\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	// The loops are created before the group is bound and the owner known,
	// so the session the creation tick minted holds a catalog from before
	// any of it (#162). Rotating gives the loop a session whose prompt was
	// rendered against the fleet as it now is.
	at := time.Now().UnixMilli()
	srv.message("alpha", "who is there")
	minted := waitPrompt(t, srv, "alpha", at)
	before := waitPromptAfterRotation(t, srv, "alpha", minted.SessionID)
	for _, want := range []string{
		"posting in telegram as @alpha_bot",
		"Your owner is @operator, but there is no private chat with",
		"@beta —",
		"The people who can talk to this fleet",
		"@mentioning a person in the group is public",
	} {
		if !strings.Contains(before, want) {
			t.Fatalf("catalog lacks %q before the owner writes:\n%s", want, before)
		}
	}

	srv.waitState("alpha", "asleep", 30*time.Second)
	tg.dm("alpha", operator, "here I am")
	waitOwnerDMReady(t, srv, "alpha")

	at = time.Now().UnixMilli()
	srv.message("alpha", "and now")
	onCapture := waitPrompt(t, srv, "alpha", at)
	after := waitPromptAfterRotation(t, srv, "alpha", onCapture.SessionID)
	if !strings.Contains(after, "Your owner is @operator; owner_dm reaches them privately") {
		t.Fatalf("catalog still reports the owner as unreachable:\n%s", after)
	}
	if strings.Contains(after, "no private chat with") {
		t.Fatalf("the stale unreachable line survived the capture:\n%s", after)
	}
}

// waitPromptAfterRotation returns the system prompt of the session a loop
// lands on after its current one is rotated on demand (#93). A resumed
// session keeps the prompt it was created with (#162), and nothing rotates a
// loop to chase a prompt change, so this is how a change reaches the system
// prompt rather than only the transcript: the operator asks, and the fresh
// session is spawned with the prompt rendered at that wake.
func waitPromptAfterRotation(t *testing.T, s *server, name, old string) string {
	t.Helper()
	s.mustJSON("POST", "/api/loops/"+name+"/rotate", nil, nil)
	at := time.Now().UnixMilli()
	s.message(name, "wake on the successor")
	tn := s.waitTurn(name, 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && tn.SessionID != old &&
			strings.Contains(tn.ResultText, "WHO YOU CAN ADDRESS")
	})
	return tn.ResultText
}

// waitPrompt returns the !sysprompt turn that replayed a loop's system
// prompt, from a turn that ended at or after notBefore. The whole turn
// rather than its text: which session replayed the prompt is half of what a
// caller needs, since a resumed session replays the one it was created with
// (#162).
func waitPrompt(t *testing.T, s *server, name string, notBefore int64) turn {
	t.Helper()
	return s.waitTurn(name, 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= notBefore && strings.Contains(tn.ResultText, "WHO YOU CAN ADDRESS")
	})
}
