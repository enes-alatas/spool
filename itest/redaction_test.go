//go:build integration

package itest

import (
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
	s := startServer(t, t.TempDir())
	s.createLoop("leaky", nil)

	const value = "ghp_itestREDACTIONvalue987654"
	s.mustJSON("PUT", "/api/loops/leaky/secrets/LEAK_TOKEN", map[string]any{"value": value}, nil)

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
		"/api/messages?limit=100",
	} {
		if _, body := s.do("GET", path, nil); strings.Contains(string(body), value) {
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

// TestASecretIsRedactedAsSoonAsItIsWritten: the redactor refreshes on the
// write rather than waiting out its ttl. Without that, a secret added while
// the process runs is in the clear for as long as the last snapshot lives —
// and the first thing a new secret does is get used.
func TestASecretIsRedactedAsSoonAsItIsWritten(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("fresh", nil)
	s.scriptLoop("fresh", "!env LATE_TOKEN")

	const value = "ghp_itestWRITTENlate1234567"
	s.mustJSON("PUT", "/api/loops/fresh/secrets/LATE_TOKEN", map[string]any{"value": value}, nil)

	s.message("fresh", "say it")
	turn := s.waitTurn("fresh", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "LATE_TOKEN=")
	})
	if strings.Contains(turn.ResultText, value) {
		t.Errorf("a secret written mid-run reached the transcript: %s", dump(turn))
	}
}
