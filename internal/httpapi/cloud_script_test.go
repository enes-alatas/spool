package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// fakeScriptDigest is the digest recordingHooks serves its script under.
var fakeScriptDigest = strings.Repeat("ab", 32)

// A taken SessionStart post records the digest its setup script sent: a
// digest as it came, none or a malformed one as store.CloudScriptNone. A
// refused post records nothing, since anything in the VM can post with
// the token (#1381).
func TestASessionStartRecordsItsSetupScriptsDigest(t *testing.T) {
	older := strings.Repeat("cd", 32)
	for _, test := range []struct {
		name, script string
		refuse       bool
		want         string
	}{
		{"current", fakeScriptDigest, false, fakeScriptDigest},
		{"older", older, false, older},
		{"none sent", "", false, store.CloudScriptNone},
		{"not a digest", "ABC; rm -rf /", false, store.CloudScriptNone},
		{"refused", fakeScriptDigest, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			hooks := &recordingHooks{}
			if test.refuse {
				hooks.refuse = errors.New("no start pending")
			}
			handler, db := hookServerStore(t, hooks, noRedaction)
			parts := [][2]string{{"hook", startInput}, {"session", "cse_01Synthetic"}}
			if test.script != "" {
				parts = append(parts, [2]string{"script", test.script})
			}
			body, contentType := hookForm(t, parts...)
			wantCode := http.StatusOK
			if test.refuse {
				wantCode = http.StatusConflict
			}
			if code := postHook(handler, cloudLoopToken, body, contentType); code != wantCode {
				t.Fatalf("SessionStart: %d, want %d", code, wantCode)
			}
			loopRecord, err := db.Loops().Get(context.Background(), "l-cloud")
			if err != nil {
				t.Fatal(err)
			}
			if loopRecord.CloudScriptDigest != test.want {
				t.Errorf("the recorded digest = %q, want %q", loopRecord.CloudScriptDigest, test.want)
			}
		})
	}
}

// The setup view's script state compares what the environment reported
// with the digest of the script the hub serves now. The digest is of the
// script's text alone, so an upgrade that leaves the text as it was
// flags no loop (#1381).
func TestCloudScriptState(t *testing.T) {
	for _, test := range []struct{ reported, want string }{
		{"", cloudScriptUnheard},
		{fakeScriptDigest, cloudScriptCurrent},
		{strings.Repeat("cd", 32), cloudScriptOutdated},
		{store.CloudScriptNone, cloudScriptOutdated},
	} {
		if got := cloudScriptState(test.reported, fakeScriptDigest); got != test.want {
			t.Errorf("cloudScriptState(%q) = %q, want %q", test.reported, got, test.want)
		}
	}
}
