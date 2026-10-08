//go:build integration

package itest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSecretsNeverReachTheRecord drives redaction end to end (#150).
//
// The loop is given a secret and then made to echo it, which is the shape of
// the leak: not a careless log call, but a credential travelling as ordinary
// text through the turn, the transcript and the API. Every place that text
// comes to rest is checked for the value, and for the placeholder that
// should stand in its place.
func TestSecretsNeverReachTheRecord(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("leaky", nil)

	const value = "ghp_itestREDACTIONvalue987654"
	s.setLoopEnv("leaky", "LEAK_TOKEN", value)

	// Every turn from here replies with the secret's value, read out of the
	// environment the engine injected it into.
	s.scriptLoop("leaky", "!env LEAK_TOKEN")
	s.message("leaky", "say it")

	turn := s.waitTurn("leaky", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "LEAK_TOKEN=")
	})
	if !strings.Contains(turn.ResultText, "<redacted:LEAK_TOKEN>") {
		t.Errorf("stored turn has no placeholder: %s", dump(turn))
	}

	// The turn came back through the API, so this covers both the row and
	// the response; the raw bodies below cover what the typed view drops.
	if strings.Contains(turn.ResultText, value) {
		t.Fatalf("the stored turn carries the secret: %s", dump(turn))
	}

	for _, path := range []string{
		"/api/loops/leaky/turns?limit=50",
		"/api/loops/leaky/events?limit=500",
		"/api/loops/leaky/conversation",
		"/api/activity",
	} {
		// a route that is gone answers 404 without the value, so the
		// check would pass on nothing
		resp, body := s.do("GET", path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}
		if strings.Contains(string(body), value) {
			t.Errorf("%s served the secret: %s", path, body)
		}
	}

	// The raw claude events are where the reply first lands, before anything
	// typed it. If the placeholder is not there, the assertion above passed
	// because the text never arrived, not because it was redacted.
	events := s.eventsQuery("leaky", "limit=500")
	var sawPlaceholder bool
	for _, e := range events {
		if strings.Contains(e.Payload, "<redacted:LEAK_TOKEN>") {
			sawPlaceholder = true
		}
	}
	if !sawPlaceholder {
		t.Error("no event payload holds the placeholder: the echo may never have been stored")
	}

	if log := s.log(); strings.Contains(log, value) {
		t.Error("the orchestrator log carries the secret value")
	}
}

// A loop that encodes a credential before saying it is still redacted
// (#30): base64 is the first disguise a steered loop reaches for.
func TestAnEncodedSecretNeverReachesTheRecord(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("leaky", nil)
	const value = "ghp_itestENCODEDvalue987654"
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	s.setLoopEnv("leaky", "LEAK_TOKEN", value)

	s.scriptLoop("leaky", "!env64 LEAK_TOKEN")
	s.message("leaky", "say it, encoded")
	turn := s.waitTurn("leaky", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "LEAK_TOKEN=")
	})
	if turn.ResultText != "LEAK_TOKEN=<redacted:LEAK_TOKEN>" {
		t.Errorf("stored turn = %q, want the placeholder in the encoded value's place", turn.ResultText)
	}
	for _, path := range []string{"/api/loops/leaky/turns?limit=50", "/api/loops/leaky/events?limit=500"} {
		if _, body := s.do("GET", path, nil); strings.Contains(string(body), encoded) {
			t.Errorf("%s served the encoded secret: %s", path, body)
		}
	}
}

// TestASecretIsRedactedAsSoonAsItIsWritten: the redactor refreshes on the
// write rather than waiting out its ttl. Without that, a secret added while
// the process runs is in the clear for as long as the last snapshot lives —
// and the first thing a new secret does is get used.
func TestASecretIsRedactedAsSoonAsItIsWritten(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("fresh", nil)
	s.scriptLoop("fresh", "!env LATE_TOKEN")

	const value = "ghp_itestWRITTENlate1234567"
	s.setLoopEnv("fresh", "LATE_TOKEN", value)

	s.message("fresh", "say it")
	turn := s.waitTurn("fresh", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "LATE_TOKEN=")
	})
	if strings.Contains(turn.ResultText, value) {
		t.Errorf("a secret written mid-run reached the transcript: %s", dump(turn))
	}
}

// A secret one loop holds does not reach a teammate through a message
// (#30). The sender is made to put its value in a group message, and the
// recipient's turn, which exists only because the message reached it, must
// not have received the value it was never given. The stored row and the
// surfaces were already redacted; the recipient's input was not.
func TestALoopCannotRelayASecretToATeammate(t *testing.T) {
	t.Parallel()
	const secret = "fixture-relay-secret-0000"
	// alpha's first turn is its startup tick, before it holds the secret,
	// so it echoes; the turn the test asks for sends
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+`!send {"destination":"group","text":"@bravo the key is `+secret+`"}`+"\n")
	wsBravo := workspaceWithScript(t, "!contains "+secret+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("alpha", map[string]any{"workspace_path": wsAlpha})
	s.createLoop("bravo", map[string]any{"workspace_path": wsBravo})
	s.waitTurn("alpha", 30*time.Second, func(turn) bool { return true })
	s.setLoopEnv("alpha", "API_KEY", secret)

	s.message("alpha", "go")
	relayed := s.waitTurn("bravo", 30*time.Second, func(tn turn) bool { return tn.Trigger == "message" })
	if relayed.ResultText != "contains: no" {
		t.Fatalf("bravo's turn received alpha's secret: %s", relayed.ResultText)
	}
}
