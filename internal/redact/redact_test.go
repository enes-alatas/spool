package redact

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeSource hands out whatever the test last set, and counts the reads so a
// test can tell a cached snapshot from a fresh one.
type fakeSource struct {
	secrets []Secret
	err     error
	reads   int
}

func (fake *fakeSource) Secrets(context.Context) ([]Secret, error) {
	fake.reads++
	return fake.secrets, fake.err
}

func loaded(t *testing.T, secrets ...Secret) (*Redactor, *fakeSource) {
	t.Helper()
	src := &fakeSource{secrets: secrets}
	redactor := New(src, 0)
	if err := redactor.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return redactor, src
}

func TestTextReplacesKnownValuesOnly(t *testing.T) {
	redactor, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: "ghp_abcdefghij"})

	if got := redactor.Text("pushed with ghp_abcdefghij, done"); got != "pushed with <redacted:GH_TOKEN>, done" {
		t.Errorf("Text = %q", got)
	}
	if got := redactor.Text("nothing secret here"); got != "nothing secret here" {
		t.Errorf("unrelated text rewritten: %q", got)
	}
}

// A redactor with no secrets loaded yet must not be mistaken for one that has
// checked: it redacts nothing, which is why wiring loads before it writes.
func TestUnloadedRedactorLeavesTextAlone(t *testing.T) {
	redactor := New(&fakeSource{secrets: []Secret{{Name: "T", Value: "sekrit-value"}}}, 0)
	if got := redactor.Text("sekrit-value"); got != "sekrit-value" {
		t.Errorf("Text = %q, want the input unchanged before the first load", got)
	}
}

// A value that contains another must be replaced whole. Replacing the
// shorter one first would leave "<redacted:short>xyz" — the outer secret
// still legible around a placeholder, which reads as redacted and is not.
func TestLongestValueWinsWhenOneContainsAnother(t *testing.T) {
	redactor, _ := loaded(t,
		Secret{Name: "short", Value: "abcdefgh"},
		Secret{Name: "long", Value: "abcdefghijklmnop"},
	)
	if got := redactor.Text("token abcdefghijklmnop end"); got != "token <redacted:long> end" {
		t.Errorf("Text = %q", got)
	}
}

func TestValuesShorterThanMinLengthAreIgnored(t *testing.T) {
	short := strings.Repeat("a", MinLength-1)
	redactor, _ := loaded(t, Secret{Name: "tiny", Value: short})
	if got := redactor.Text("a caravan of " + short + "s"); strings.Contains(got, "<redacted") {
		t.Errorf("Text = %q, want a value under MinLength left alone", got)
	}
}

func TestRefreshPicksUpASecretAddedLater(t *testing.T) {
	redactor, src := loaded(t)
	src.secrets = []Secret{{Name: "NEW", Value: "brand-new-value"}}

	if got := redactor.Text("brand-new-value"); got != "brand-new-value" {
		t.Fatalf("Text = %q, want the pre-refresh snapshot", got)
	}
	if err := redactor.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := redactor.Text("brand-new-value"); got != "<redacted:NEW>" {
		t.Errorf("Text = %q after refresh", got)
	}
}

// The ttl is the backstop for a write that never called Refresh: the next
// reader reloads once the snapshot is older than it.
func TestSnapshotReloadsOnceTheTTLPasses(t *testing.T) {
	src := &fakeSource{}
	now := time.Now()
	redactor := New(src, time.Minute)
	redactor.now = func() time.Time { return now }
	if err := redactor.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	src.secrets = []Secret{{Name: "LATE", Value: "arrived-late-value"}}
	if got := redactor.Text("arrived-late-value"); got != "arrived-late-value" {
		t.Fatalf("reloaded before the ttl passed: %q", got)
	}

	now = now.Add(time.Minute)
	if got := redactor.Text("arrived-late-value"); got != "<redacted:LATE>" {
		t.Errorf("Text = %q, want a reload once the ttl passed", got)
	}
	reads := src.reads
	if got := redactor.Text("arrived-late-value"); got != "<redacted:LATE>" || src.reads != reads {
		t.Errorf("reloaded again inside the ttl: %d reads", src.reads)
	}
}

// A store that cannot answer must not turn redaction off. Losing the
// snapshot would leak every secret in it until the store came back.
func TestAFailedReloadKeepsTheLastGoodSnapshot(t *testing.T) {
	redactor, src := loaded(t, Secret{Name: "KEEP", Value: "keep-me-hidden"})
	src.err = errors.New("database is locked")

	if err := redactor.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh returned nil on a failing source")
	}
	if got := redactor.Text("keep-me-hidden"); got != "<redacted:KEEP>" {
		t.Errorf("Text = %q, want the previous snapshot to survive a failed reload", got)
	}
}

func TestRedactedReportsAHit(t *testing.T) {
	redactor, _ := loaded(t, Secret{Name: "T", Value: "value-of-a-token"})
	if !redactor.Redacted("carrying value-of-a-token along") {
		t.Error("Redacted = false on text holding a secret")
	}
	if redactor.Redacted("carrying nothing along") {
		t.Error("Redacted = true on clean text")
	}
}

// A secret's usual encodings are the same credential, and redacted as the
// value is (#30): base64 in either alphabet, at any alignment inside a
// longer encoded text, hex in either case, and percent-encoding.
func TestTextReplacesEncodedForms(t *testing.T) {
	const value = "fixture/token+value?0000"
	redactor, _ := loaded(t, Secret{Name: "API_KEY", Value: value})
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + value))
	for name, text := range map[string]string{
		"base64":            base64.StdEncoding.EncodeToString([]byte(value)),
		"base64 unpadded":   base64.RawStdEncoding.EncodeToString([]byte(value)),
		"base64url":         base64.URLEncoding.EncodeToString([]byte(value)),
		"base64 offset one": base64.StdEncoding.EncodeToString([]byte("a" + value)),
		"base64 offset two": base64.StdEncoding.EncodeToString([]byte("ab" + value)),
		"basic auth header": "Authorization: Basic " + basic,
		"hex":               hex.EncodeToString([]byte(value)),
		"hex upper":         strings.ToUpper(hex.EncodeToString([]byte(value))),
		"query escaped":     "https://example.com/?k=" + url.QueryEscape(value),
		"path escaped":      "https://example.com/" + url.PathEscape(value),
	} {
		if !redactor.Redacted(text) {
			t.Errorf("%s: %q was not redacted", name, text)
		}
	}
	// what remains around the match is the encoding of the neighbours, not
	// of the value: nothing of it decodes back
	if got := redactor.Text(basic); !strings.Contains(got, "<redacted:API_KEY>") {
		t.Errorf("Basic header = %q, want the placeholder inside it", got)
	}
}

// The forms are of the value's bytes alone, so ordinary text, and the
// encodings of other text, pass through untouched.
func TestEncodedFormsLeaveOtherTextAlone(t *testing.T) {
	redactor, _ := loaded(t, Secret{Name: "API_KEY", Value: "fixture-token-0000"})
	for _, text := range []string{
		"nothing secret here",
		base64.StdEncoding.EncodeToString([]byte("an ordinary sentence, encoded")),
		hex.EncodeToString([]byte("an ordinary sentence, encoded")),
	} {
		if redactor.Redacted(text) {
			t.Errorf("%q was redacted", text)
		}
	}
}
