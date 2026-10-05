//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// Rotating a connection's value ends the session that ran with the old one
// (#609): the loop takes a handoff turn on it, and the fresh session's wake
// holds the new value. The old value stays redacted, setting the value held
// changes nothing, and the record says rotate. The marker value is shorter
// than the redactor acts on, so every turn can show the value it ran with.
func TestRotatingAConnectionEndsItsSessions(t *testing.T) {
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
		"name": "marker", "kind": "env-var", "config": map[string]any{"env": "MARKER"}, "secret": "old",
	}, nil)
	for _, name := range []string{"script", "marker"} {
		s.mustJSON("PUT", "/api/loops/aster/connections/"+name, nil, nil)
	}

	s.message("aster", "first")
	first := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.ResultText == "MARKER=old" })
	s.waitState("aster", "asleep", 30*time.Second)

	var rotated connectionJSON
	s.mustJSON("PUT", "/api/connections/marker/secret", map[string]any{"value": "new"}, &rotated)
	if rotated.RotatedAt == 0 {
		t.Fatalf("marker after its rotation = %+v, want rotated_at set", rotated)
	}
	handoff := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.Trigger == "rotation" })
	if handoff.SessionID != first.SessionID {
		t.Fatalf("the handoff ran on session %s, want the one that held the old value, %s", handoff.SessionID, first.SessionID)
	}
	// The loop is told a credential was replaced, so its note leaves the
	// old value out, not that its context is full. The envelope wraps, so
	// match it on single spaces.
	told := strings.Join(strings.Fields(strings.Join(s.turnInputs("aster")[handoff.ID], "\n")), " ")
	if !strings.Contains(told, "The operator replaced a credential you hold") ||
		!strings.Contains(told, "Leave every credential value out of your note") ||
		strings.Contains(told, "filling up") {
		t.Fatalf("a connection rotation told the loop the wrong cause:\n%s", told)
	}
	s.waitState("aster", "asleep", 30*time.Second)

	s.message("aster", "second")
	second := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return tn.SessionID != first.SessionID && strings.Contains(tn.ResultText, "MARKER=")
	})
	if second.ResultText != "MARKER=new" {
		t.Fatalf("the fresh session's turn = %q, want MARKER=new", second.ResultText)
	}
	s.waitState("aster", "asleep", 30*time.Second)

	// The value held again: no rotation, no row, no handoff.
	var again connectionJSON
	s.mustJSON("PUT", "/api/connections/marker/secret", map[string]any{"value": "new"}, &again)
	if again.RotatedAt != rotated.RotatedAt {
		t.Errorf("rotated_at after setting the value held = %d, want %d", again.RotatedAt, rotated.RotatedAt)
	}
	s.wantRefusal("PUT", "/api/connections/marker/secret", map[string]any{"value": ""}, 400, "connection_secret_invalid")
	s.wantRefusal("PUT", "/api/connections/nowhere/secret", map[string]any{"value": "x"}, 404, "connection_not_found")

	// A long value replaced stays redacted: a credential the hub let go of
	// may still be live upstream.
	const retired = "ghp_fixtureRETIREDvalue0000"
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": retired,
	}, nil)
	s.mustJSON("PUT", "/api/connections/github/secret", map[string]any{"value": "ghp_fixtureCURRENTvalue0000"}, nil)
	s.mustJSON("POST", "/api/channels/group/messages", map[string]any{"text": "the old key is " + retired}, nil)
	var said []struct {
		Text string `json:"text"`
	}
	s.mustJSON("GET", "/api/channels/group/messages", nil, &said)
	if len(said) != 1 || said[0].Text != "the old key is <redacted:GH_TOKEN>" {
		t.Errorf("a retired value was not redacted from a message: %+v", said)
	}

	got := s.connectionRecord("/api/connections/marker/events")
	if want := []string{"create marker", "attach marker aster", "rotate marker"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("marker's record = %q, want %q", got, want)
	}
	rotations := 0
	for _, tn := range s.completed("aster") {
		if tn.Trigger == "rotation" {
			rotations++
		}
	}
	if rotations != 1 {
		t.Errorf("aster ran %d handoff turns, want the one rotation", rotations)
	}
}
