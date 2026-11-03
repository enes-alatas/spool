//go:build integration

package itest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloud loop keeps one claude.ai session through a rotation and a pause
// (#1278). A rotation by choice sends /clear into the session the last hub
// session ran in, which posts SessionStart with source clear, answered
// with the loop's prompt again, and the new hub session's first turn runs
// there. A pause and a resume carry on in it. A pause across which a
// connection is attached starts a session of its own, and the loop's
// events say why: the old session's shell holds the old set. A rotation
// there hands the connections over again: /clear starts the shell's env
// file anew.
func TestACloudLoopKeepsItsSessionThroughARotationAndAPause(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cumulus")
	hub.message("cumulus", "first words")
	first := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "first words") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	vm := filepath.Join(hub.fkState, "cloud", vms[0])

	hub.mustJSON("POST", "/api/loops/cumulus/rotate", nil, nil)
	within(t, 30*time.Second, "the rotation", func() bool { return strings.Contains(hub.log(), "context rotated") })
	hub.waitState("cumulus", "asleep", 20*time.Second)
	hub.message("cumulus", "after the rotation")
	rotated := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "after the rotation") })
	if rotated.IsError || rotated.SessionID == first.SessionID {
		t.Fatalf("the turn after the rotation: %+v, want one in a new hub session", rotated)
	}
	if got := cloudVMs(t, hub); len(got) != 1 {
		t.Fatalf("the rotation left %d sessions, want it carried on in the one", len(got))
	}
	if cleared, _ := os.ReadFile(filepath.Join(vm, "cleared")); string(cleared) != "1" {
		t.Errorf("the session was cleared %q times, want once", cleared)
	}
	if context, _ := os.ReadFile(filepath.Join(vm, "context")); !strings.Contains(string(context), "cumulus") {
		t.Errorf("the cleared session's context is %q, want the hub's prompt again", context)
	}
	within(t, 10*time.Second, "the clear's post logged as answered", func() bool {
		return strings.Contains(hub.log(), "a cleared session was handed its loop's connections and prompt")
	})

	hub.mustJSON("POST", "/api/loops/cumulus/pause", nil, nil)
	hub.mustJSON("POST", "/api/loops/cumulus/resume", nil, nil)
	hub.message("cumulus", "after the pause")
	resumed := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "after the pause") })
	if resumed.IsError || resumed.SessionID != rotated.SessionID || len(cloudVMs(t, hub)) != 1 {
		t.Fatalf("the turn after the pause: %+v in %d sessions, want it carried on in the one", resumed, len(cloudVMs(t, hub)))
	}

	hub.waitState("cumulus", "asleep", 20*time.Second)
	hub.mustJSON("POST", "/api/loops/cumulus/pause", nil, nil)
	if resp, body := hub.do("POST", "/api/connections", map[string]any{
		"name": "github", "kind": "env-var", "config": map[string]any{"env": "GH_TOKEN"}, "secret": "synthetic-gh-value",
	}); resp.StatusCode != 201 {
		t.Fatalf("creating the connection: %d %s", resp.StatusCode, body)
	}
	if resp, body := hub.do("PUT", "/api/loops/cumulus/connections/github", nil); resp.StatusCode != 204 {
		t.Fatalf("attaching the connection: %d %s", resp.StatusCode, body)
	}
	hub.mustJSON("POST", "/api/loops/cumulus/resume", nil, nil)
	hub.message("cumulus", "with a connection")
	moved := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "with a connection") })
	if moved.IsError || len(cloudVMs(t, hub)) != 2 {
		t.Fatalf("the turn after attaching a connection: %+v in %d sessions, want a session of its own", moved, len(cloudVMs(t, hub)))
	}
	var events []spoolEvent
	hub.mustJSON("GET", "/api/loops/cumulus/events?limit=200", nil, &events)
	said := false
	for _, event := range events {
		said = said || (event.Subtype == "session_lost" && strings.Contains(event.Payload, "connections changed"))
	}
	if !said {
		t.Errorf("the loop's events don't say why it left its session: %+v", events)
	}

	// /clear empties the env file, so the clear's answer brings them back
	var secondVM string
	for _, name := range cloudVMs(t, hub) {
		if name != vms[0] {
			secondVM = filepath.Join(hub.fkState, "cloud", name)
		}
	}
	hub.waitState("cumulus", "asleep", 20*time.Second)
	hub.mustJSON("POST", "/api/loops/cumulus/rotate", nil, nil)
	within(t, 30*time.Second, "the second rotation", func() bool { return strings.Count(hub.log(), "context rotated") >= 2 })
	hub.waitState("cumulus", "asleep", 20*time.Second)
	hub.message("cumulus", "after another rotation\n!envsum GH_TOKEN")
	sum := sha256.Sum256([]byte("synthetic-gh-value"))
	want := "GH_TOKEN=" + hex.EncodeToString(sum[:])
	seen := hub.waitTurn("cumulus", 30*time.Second, func(turn turn) bool { return strings.HasPrefix(turn.ResultText, "GH_TOKEN=") })
	if seen.ResultText != want || len(cloudVMs(t, hub)) != 2 {
		t.Errorf("after a rotation the shell sees %q in %d sessions, want %q in the one carried on", seen.ResultText, len(cloudVMs(t, hub)), want)
	}
	if cleared, _ := os.ReadFile(filepath.Join(secondVM, "cleared")); string(cleared) != "1" {
		t.Errorf("the connected session was cleared %q times, want once", cleared)
	}
}
