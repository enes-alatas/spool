//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime/cloud"
)

// A cloud loop's setup view says whether its environment runs the setup
// script the hub serves now, from the digest each session start sends
// (#1381). A session under the current script reads as current. One under
// a script from before scripts sent a digest reads as outdated, and so
// does one under an older script, after an upgrade that changed it: the
// view carries the new script to paste, and the loop keeps taking turns,
// since an older script still runs them.
func TestACloudLoopsSetupViewSaysWhenItsScriptIsOutOfDate(t *testing.T) {
	t.Parallel()
	hub := startCloudHub(t, "cirrus")
	current, err := cloud.SetupScript(hub.cloudURL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := cloud.ScriptDigest(hub.cloudURL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	setupFile := filepath.Join(hub.fkState, "environments", cloudEnvironmentOf("cirrus"), "setup.sh")
	writeSetup := func(script string) {
		t.Helper()
		if err := os.WriteFile(setupFile, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// the harness's check ran under the script the hub serves now
	var view cloudLoopSetup
	hub.mustJSON("GET", "/api/loops/cirrus/cloud/setup", nil, &view)
	if view.ScriptState != "current" {
		t.Errorf("after a check under the current script, script_state = %q, want current", view.ScriptState)
	}

	// a script from before this change sends no digest
	unreported := strings.Replace(current, ` --form-string "script=$digest"`, "", 1)
	if unreported == current {
		t.Fatal("the setup script sends no digest to take out")
	}
	writeSetup(unreported)
	if view = checkCloudLoop(t, hub, "cirrus"); view.ScriptState != "outdated" || view.Script != current {
		t.Errorf("under a script that reports no digest: script_state %q, want outdated with the current script", view.ScriptState)
	}
	if view.Check == nil || view.Check.State != "ok" {
		t.Errorf("the check under that script = %+v, want a pass: the script only warns", view.Check)
	}

	// pasted again, the script reads as current at the next session start
	writeSetup(current)
	if view = checkCloudLoop(t, hub, "cirrus"); view.ScriptState != "current" {
		t.Errorf("after pasting the current script again, script_state = %q, want current", view.ScriptState)
	}

	// an older script, as after an upgrade that changed it, sends its own
	writeSetup(strings.Replace(current, digest, strings.Repeat("0", 63)+"1", 1))
	hub.message("cirrus", "words under an older script")
	got := hub.waitTurn("cirrus", 30*time.Second, func(turn turn) bool { return strings.Contains(turn.ResultText, "older script") })
	if got.IsError {
		t.Fatalf("the turn under an older script: %+v, want it taken", got)
	}
	hub.mustJSON("GET", "/api/loops/cirrus/cloud/setup", nil, &view)
	if view.ScriptState != "outdated" || view.Script != current {
		t.Errorf("under an older script: script_state %q, want outdated with the current script", view.ScriptState)
	}
	for _, step := range view.Steps {
		if step == "check" {
			t.Errorf("an outdated script held the loop: steps %v", view.Steps)
		}
	}
}
