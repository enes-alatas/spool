package cloud

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// rotatedSpec is the hub session that succeeds testSpec's by a rotation.
func rotatedSpec() runtime.Spec {
	spec := testSpec()
	spec.SessionID, spec.Rotated = "hub-session-2", true
	return spec
}

// A rotation's successor carries on in the cloud session the last hub
// session ran in (#1278): its first turn sends /clear there, waits for the
// session's clear post, answers it with the loop's connections and
// prompt, and then sends the turn into the same session.
func TestARotationClearsTheSessionItRanIn(t *testing.T) {
	args := t.TempDir() + "/args"
	cloud := cliRuntime(t, `case "$1" in
--cloud) sleep 30 ;;
-p) echo "$*" >> `+args+`; echo '{"ok":true}' ;;
esac
`)
	startedSession(t, cloud)

	proc := startCLI(t, cloud, rotatedSpec())
	answered := make(chan []byte, 1)
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if answer, err := cloud.SessionStart(context.Background(), "loop-1", "session_01Synthetic", []byte(`{"source":"clear"}`)); err == nil {
				answered <- answer
				break
			}
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if cloud.Stop(context.Background(), "loop-1", "session_01Synthetic", []byte(`{"last_assistant_message":"cleared and on"}`), nil, false) == nil {
				return
			}
		}
	}()
	if _, result := turnResult(t, proc, "second words"); result.IsError || result.ResultText != "cleared and on" {
		t.Fatalf("the rotated turn: %+v", result)
	}
	if sent, _ := os.ReadFile(args); string(sent) != "-p /clear --cloud session_01Synthetic --output-format json\n"+
		"-p second words --cloud session_01Synthetic --output-format json\n" {
		t.Errorf("the rotation sent %q, want /clear and then the turn into the session it ran in", sent)
	}
	select {
	case answer := <-answered:
		if !strings.Contains(string(answer), "You are cirrus.") {
			t.Errorf("the clear post was answered %s, want the loop's prompt", answer)
		}
	default:
		t.Error("the clear post wasn't answered")
	}
	if current, _ := cloud.load("loop-1"); current.SessionID != "session_01Synthetic" || current.ClaudeSession != "hub-session-2" {
		t.Errorf("the state after the rotation: %+v, want the same cloud session under the new hub session", current)
	}
}

// A clear post the hub sent no /clear for is answered as a compaction's,
// with the prompt alone, and only while a turn is in flight.
func TestAClearPostTheHubDidntAskForGetsNoConnections(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	startedSession(t, cloud)
	if answer, ok := cloud.answerClear("loop-1", "session_01Synthetic"); ok {
		t.Errorf("an unasked clear post was answered %s", answer)
	}
}

// A session isn't carried on, by a resume or a rotation, when the hub
// can't tell what it holds: a turn whose Stop post never came, or
// connections changed since it started. A resume of one ends as lost and
// says why; a rotation starts a session of its own.
func TestASessionLeftUnheardOrWithOtherConnectionsIsNotCarriedOn(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *Runtime) runtime.Spec
		why    string
	}{
		{"a turn cut off", func(t *testing.T, cloud *Runtime) runtime.Spec {
			if err := cloud.update("loop-1", func(current *state) {
				current.Turn = &turnState{Since: 1, Until: 2, SessionURL: current.SessionURL, CutOff: true}
			}); err != nil {
				t.Fatal(err)
			}
			return resumeSpec(testSource)
		}, "cut off"},
		{"a connection attached", func(*testing.T, *Runtime) runtime.Spec {
			spec := resumeSpec(testSource)
			spec.Env = map[string]string{"GH_TOKEN": "synthetic-value"}
			return spec
		}, "connections changed"},
		{"a state from before #1278", func(t *testing.T, cloud *Runtime) runtime.Spec {
			if err := cloud.update("loop-1", func(current *state) { current.Connections = "" }); err != nil {
				t.Fatal(err)
			}
			return resumeSpec(testSource)
		}, "connections changed"},
		{"a replaced hub key", func(_ *testing.T, cloud *Runtime) runtime.Spec {
			// the state keeps a MAC under the hub key, not a bare hash
			// of the values (#1293), so a new key tells no set apart
			cloud.HubKey = []byte("another-synthetic-hub-key")
			return resumeSpec(testSource)
		}, "connections changed"},
		{"no hub key", func(_ *testing.T, cloud *Runtime) runtime.Spec {
			cloud.HubKey = nil
			return resumeSpec(testSource)
		}, "connections changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cloud := cliRuntime(t, `sleep 30`)
			startedSession(t, cloud)
			spec := test.change(t, cloud)
			proc := startCLI(t, cloud, spec)
			exit := proc.Wait()
			if !claude.IsSessionNotFound(exit) || !strings.Contains(exit.Stderr, test.why) {
				t.Errorf("the resume ended %+v, want lost, saying %q", exit, test.why)
			}

			spec.SessionID, spec.ResumeID, spec.Rotated = "hub-session-2", "", true
			rotated, err := cloud.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = rotated.Kill(); rotated.Wait() })
			if session := rotated.(*cliSession); session.clearFirst || session.sessionID != "" {
				t.Errorf("the rotation carries on in %q (clear first: %v), want a session of its own", session.sessionID, session.clearFirst)
			}
		})
	}
}

// A fresh hub session that no rotation asked for starts a cloud session
// of its own, as a kill's next start does.
func TestAFreshSessionNotRotatedStartsAnew(t *testing.T) {
	cloud := cliRuntime(t, `sleep 30`)
	startedSession(t, cloud)
	spec := testSpec()
	spec.SessionID = "hub-session-2"
	proc, err := cloud.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Kill(); proc.Wait() })
	if session := proc.(*cliSession); session.clearFirst || session.sessionID != "" {
		t.Errorf("an unrotated fresh session carries on in %q, want one of its own", session.sessionID)
	}
}
