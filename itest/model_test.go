//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A model the API does not know fails every turn the same way, free and
// fast, so the loop holds rather than retrying, and says why (#289). What
// it is told meanwhile waits, across a restart too. Saving the model again
// checks it again; an edit of the model clears the refusal and wakes the
// loop on the new one.
func TestAnUnrecognizedModelHoldsTheLoopUntilTheModelIsEdited(t *testing.T) {
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("aster", map[string]any{"model": "claude-nosuch-1"})

	s.waitState("aster", "model_unrecognized", 30*time.Second)
	v := s.loop("aster")
	if !strings.Contains(v.ModelRefusal, "(claude-nosuch-1)") {
		t.Fatalf("model_refusal = %q, want the CLI's sentence naming the model", v.ModelRefusal)
	}
	if v.ResolvedModel != "claude-nosuch-1" {
		t.Fatalf("resolved_model = %q, want what the CLI reported at init", v.ResolvedModel)
	}

	turnsBefore := len(s.completed("aster"))
	s.message("aster", "are you there")
	s.mustJSON("POST", "/api/loops/aster/wake", nil, nil)
	time.Sleep(2 * time.Second) // let a wrong wake land before looking
	if got := len(s.completed("aster")); got != turnsBefore {
		t.Fatalf("a loop on a refused model took %d more turn(s)", got-turnsBefore)
	}

	// The refusal is on the row, not only in the actor.
	s.stop()
	s = startServer(t, dataDir)
	s.waitState("aster", "model_unrecognized", 30*time.Second)

	// Saving the same model again is a retry (access may have been granted),
	// and the API and the loop must agree on how it went.
	refused := len(s.completed("aster"))
	s.mustJSON("PATCH", "/api/loops/aster", map[string]any{"model": "claude-nosuch-1"}, nil)
	deadline := time.Now().Add(30 * time.Second)
	for len(s.completed("aster")) == refused && time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
	}
	if len(s.completed("aster")) == refused {
		t.Fatal("re-saving the refused model took no turn to check it")
	}
	s.waitState("aster", "model_unrecognized", 30*time.Second)
	if v = s.loop("aster"); v.ModelRefusal == "" {
		t.Fatalf("held again as %q with no model_refusal on the loop", v.State)
	}

	s.mustJSON("PATCH", "/api/loops/aster", map[string]any{"model": "haiku"}, nil)
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "are you there")
	})
	v = s.loop("aster")
	if v.ModelRefusal != "" || v.State == "model_unrecognized" {
		t.Fatalf("after the model edit: state %q, refusal %q; want neither", v.State, v.ModelRefusal)
	}
	if v.ResolvedModel != "haiku" {
		t.Fatalf("resolved_model = %q, want the edited model's", v.ResolvedModel)
	}
}

// A refusal belongs to the model the turn ran on (#289). An operator who
// spots a typo and fixes it while the creation turn is still running must
// not find the loop held on the corrected model: the refusal that arrives
// after the edit holds nothing, and the turn runs again on the new model.
func TestARefusalOfAModelEditedMidTurnHoldsNothing(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"model": "claude-nosuch-slow-1"})

	s.waitState("aster", "busy", 10*time.Second) // inside the slow refusal
	s.mustJSON("PATCH", "/api/loops/aster", map[string]any{"model": "haiku"}, nil)

	s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError })
	v := s.loop("aster")
	if v.ModelRefusal != "" || v.State == "model_unrecognized" {
		t.Fatalf("after an edit that raced the refusal: state %q, refusal %q; want neither", v.State, v.ModelRefusal)
	}
	if v.ResolvedModel != "haiku" {
		t.Fatalf("resolved_model = %q, want the edited model's", v.ResolvedModel)
	}
	// The run took the race: the refusal arrived after the edit.
	raced := false
	for _, e := range s.eventsQuery("aster", "limit=500") {
		if e.Type == "spool" && e.Subtype == "model_retry" && strings.Contains(e.Payload, `"refused":"claude-nosuch-slow-1"`) {
			raced = true
		}
	}
	if !raced {
		t.Fatal("no model_retry naming the refused model: the edit did not race the refused turn")
	}
}
