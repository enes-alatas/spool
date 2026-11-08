//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cloud loop's session runs on the loop's model (#1281). The CLI fixes
// a session's model when it creates it, from --model, so a start names
// the loop's model, and a loop that names none runs on the account's
// default. A start that would carry on a session made on another model
// starts a session of its own, and the loop's events say why. A pause
// and a rotation then carry the session on, on the same model.
func TestACloudLoopsSessionRunsOnTheLoopsModel(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "stratus")
	hub.message("stratus", "on the default")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "on the default") })
	vms := cloudVMs(t, hub)
	if len(vms) != 1 {
		t.Fatalf("one start made %d sessions, want 1", len(vms))
	}
	if args := cloudStartArgs(t, hub, vms[0]); strings.Contains(args, "--model") {
		t.Errorf("a loop with no model started its session with %q, want no --model", args)
	}
	if got := resolvedModel(t, hub, "stratus"); got != "claude-opus-5-5" {
		t.Errorf("a loop with no model ran on %q, want the fake's default", got)
	}

	hub.waitState("stratus", "asleep", 20*time.Second)
	hub.mustJSON("POST", "/api/loops/stratus/pause", nil, nil)
	hub.mustJSON("PATCH", "/api/loops/stratus", map[string]any{"model": "claude-haiku-5-5"}, nil)
	hub.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	hub.message("stratus", "on haiku")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "on haiku") })
	vms = cloudVMs(t, hub)
	if len(vms) != 2 {
		t.Fatalf("a model change left %d sessions, want a session of its own", len(vms))
	}
	var created string
	for _, vm := range vms {
		if strings.Contains(cloudStartArgs(t, hub, vm), "--model\nclaude-haiku-5-5\n") {
			created = vm
		}
	}
	if created == "" {
		t.Fatalf("no session was started with --model claude-haiku-5-5")
	}
	if got := resolvedModel(t, hub, "stratus"); got != "claude-haiku-5-5" {
		t.Errorf("after the model change the loop ran on %q, want claude-haiku-5-5", got)
	}
	var events []spoolEvent
	hub.mustJSON("GET", "/api/loops/stratus/events?limit=200", nil, &events)
	said := false
	for _, event := range events {
		said = said || (event.Subtype == "session_lost" && strings.Contains(event.Payload, "another model"))
	}
	if !said {
		t.Errorf("the loop's events don't say why it left its session: %+v", events)
	}

	hub.waitState("stratus", "asleep", 20*time.Second)
	hub.mustJSON("POST", "/api/loops/stratus/pause", nil, nil)
	hub.mustJSON("POST", "/api/loops/stratus/resume", nil, nil)
	hub.message("stratus", "after the pause")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "after the pause") })
	hub.waitState("stratus", "asleep", 20*time.Second)
	hub.mustJSON("POST", "/api/loops/stratus/rotate", nil, nil)
	within(t, 30*time.Second, "the rotation", func() bool { return strings.Contains(hub.log(), "context rotated") })
	hub.waitState("stratus", "asleep", 20*time.Second)
	hub.message("stratus", "after the rotation")
	hub.waitTurn("stratus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "after the rotation") })
	if got := cloudVMs(t, hub); len(got) != 2 {
		t.Errorf("a pause and a rotation on the same model left %d sessions, want the one carried on", len(got))
	}
	if got := resolvedModel(t, hub, "stratus"); got != "claude-haiku-5-5" {
		t.Errorf("after the rotation the loop ran on %q, want claude-haiku-5-5", got)
	}
}

// cloudStartArgs is the argument list the fake's --cloud was started
// with for the session vm, one per line.
func cloudStartArgs(t *testing.T, hub *server, vm string) string {
	t.Helper()
	args, err := os.ReadFile(filepath.Join(hub.fkState, "cloud", vm, "cli-args"))
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

// resolvedModel is the model the loop's latest turn reported.
func resolvedModel(t *testing.T, hub *server, name string) string {
	t.Helper()
	var view struct {
		ResolvedModel string `json:"resolved_model"`
	}
	hub.mustJSON("GET", "/api/loops/"+name, nil, &view)
	return view.ResolvedModel
}
