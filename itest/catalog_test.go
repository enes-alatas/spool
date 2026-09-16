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
		if !strings.Contains(first, want) {
			t.Fatalf("a lone, ownerless loop's catalog lacks %q:\n%s", want, first)
		}
	}

	s.waitState("aster", "asleep", 30*time.Second)
	s.createLoop("briar", map[string]any{"mission": "review what aster writes"})
	at = time.Now().UnixMilli()

	s.message("aster", "and now")
	second := waitPrompt(t, s, "aster", at)
	if !strings.Contains(second, "@briar — review what aster writes") {
		t.Fatalf("a peer added since the last wake is missing:\n%s", second)
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

	at := time.Now().UnixMilli()
	srv.message("alpha", "who is there")
	before := waitPrompt(t, srv, "alpha", at)
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
	after := waitPrompt(t, srv, "alpha", at)
	if !strings.Contains(after, "Your owner is @operator; owner_dm reaches them privately") {
		t.Fatalf("catalog still reports the owner as unreachable:\n%s", after)
	}
	if strings.Contains(after, "no private chat with") {
		t.Fatalf("the stale unreachable line survived the capture:\n%s", after)
	}
}

// waitPrompt returns the system prompt a !sysprompt turn replayed, from a
// turn that ended at or after notBefore.
func waitPrompt(t *testing.T, s *server, name string, notBefore int64) string {
	t.Helper()
	tn := s.waitTurn(name, 20*time.Second, func(tn turn) bool {
		return tn.EndedAt >= notBefore && strings.Contains(tn.ResultText, "WHO YOU CAN ADDRESS")
	})
	return tn.ResultText
}
