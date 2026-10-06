//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// Revoking a connection takes it from every loop that holds it (#610): the
// loop takes a handoff turn on the session that ran with the value, and the
// fresh session's wake runs without it. The value stays redacted, the
// connection is refused from then on, and the record shows each detach
// before the revoke. The marker value is shorter than the redactor acts
// on, so every turn can show the value it ran with.
func TestRevokingAConnectionTakesItFromItsLoops(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	// its first wake's process ran without them, and a process keeps the
	// env it was spawned with
	s.waitTurn("aster", 30*time.Second, func(turn) bool { return true })
	s.waitState("aster", "asleep", 30*time.Second)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "script", "kind": "env-var", "config": map[string]any{"env": "FAKECLAUDE_SCRIPT"},
		"secret": "!env MARKER",
	}, nil)
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "marker", "kind": "env-var", "config": map[string]any{"env": "MARKER"}, "secret": "held",
	}, nil)
	for _, name := range []string{"script", "marker"} {
		s.mustJSON("PUT", "/api/loops/aster/connections/"+name, nil, nil)
	}

	s.message("aster", "first")
	first := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=held" })
	s.waitState("aster", "asleep", 30*time.Second)

	var revoked connectionJSON
	s.mustJSON("POST", "/api/connections/marker/revoke", nil, &revoked)
	if revoked.RevokedAt == 0 || revoked.HasSecret || len(revoked.Loops) != 0 {
		t.Fatalf("marker after its revoke = %+v, want revoked_at set, no value, no loops", revoked)
	}
	handoff := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.Trigger == "rotation" })
	if handoff.SessionID != first.SessionID {
		t.Fatalf("the handoff ran on session %s, want the one that held the value, %s", handoff.SessionID, first.SessionID)
	}
	// The loop is told a credential was revoked, so its note leaves the
	// value out and its successor knows it is gone. The envelope wraps, so
	// match it on single spaces.
	told := strings.Join(strings.Fields(strings.Join(s.turnInputs("aster")[handoff.ID], "\n")), " ")
	if !strings.Contains(told, "The operator revoked a credential you held") ||
		!strings.Contains(told, "Leave every credential value out of your note") ||
		strings.Contains(told, "filling up") {
		t.Fatalf("a revoke told the loop the wrong cause:\n%s", told)
	}
	s.waitState("aster", "asleep", 30*time.Second)

	s.message("aster", "second")
	second := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return tn.SessionID != first.SessionID && strings.Contains(tn.ResultText, "MARKER=")
	})
	if second.ResultText != "MARKER=" {
		t.Fatalf("the fresh session's turn = %q, want MARKER unset", second.ResultText)
	}
	s.waitState("aster", "asleep", 30*time.Second)

	// Refused from then on, and nothing revives it.
	s.wantRefusal("PUT", "/api/loops/aster/connections/marker", nil, 409, "connection_revoked")
	s.wantRefusal("PUT", "/api/connections/marker/secret", map[string]any{"value": "again"}, 409, "connection_revoked")
	s.wantRefusal("POST", "/api/connections/marker/revoke", nil, 409, "connection_revoked")
	s.wantRefusal("POST", "/api/connections/nowhere/revoke", nil, 404, "connection_not_found")

	// A long value revoked stays redacted: it may still be live upstream.
	const value = "ghp_fixtureREVOKEDvalue0000"
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": value,
	}, nil)
	s.mustJSON("POST", "/api/connections/github/revoke", nil, nil)
	s.wantRefusal("POST", "/api/connections/github/share", nil, 409, "connection_revoked")
	s.mustJSON("POST", "/api/channels/group/messages", map[string]any{"text": "the revoked key is " + value}, nil)
	var said []struct {
		Text string `json:"text"`
	}
	s.mustJSON("GET", "/api/channels/group/messages", nil, &said)
	if len(said) != 1 || said[0].Text != "the revoked key is <redacted:GH_TOKEN>" {
		t.Errorf("a revoked value was not redacted from a message: %+v", said)
	}
	// Deleting one after needs no detach.
	s.mustJSON("DELETE", "/api/connections/github", nil, nil)

	got := s.connectionRecord("/api/connections/marker/events")
	if want := []string{"create marker", "attach marker aster", "detach marker aster", "revoke marker"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("marker's record = %q, want %q", got, want)
	}
}
