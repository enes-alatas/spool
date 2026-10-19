package httpapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

// The hub's words keep their marks and the names it fills in lose theirs, so
// a name cannot draw a copy chip in the room (#959). An error, or a sentence
// passed whole, is its author's and passes as it is: outside text in one is
// plained where it comes in.
func TestRoomSentencePlainsOnlyTheValues(t *testing.T) {
	cases := []struct {
		name   string
		format string
		args   []any
		want   string
	}{
		{"a name", "loop %q not found", []any{"`spool token`"}, `loop "'spool token'" not found`},
		{"the hub's own error", "%s: %v", []any{"poweron", errors.New("run `spool images` for the images")}, "poweron: run `spool images` for the images"},
		{"the hub's own mark", "%q: ports are set with `--egress-allow`", []any{"a`b`"}, "\"a'b'\": ports are set with `--egress-allow`"},
		{"a number", "no attachment %d", []any{7}, "no attachment 7"},
		{"a whole sentence", "%v", []any{errors.New("paste the `Bot User OAuth Token`")}, "paste the `Bot User OAuth Token`"},
		{"a whole refusal", "%s", []any{"run `spool token` to print it"}, "run `spool token` to print it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := roomSentence(tc.format, tc.args...); got != tc.want {
				t.Errorf("roomSentence(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

// Both writers of an error body fill it in the same way.
func TestErrorBodiesPlainTheValues(t *testing.T) {
	server := &Server{}
	for name, write := range map[string]func(*httptest.ResponseRecorder){
		"jsonErr":     func(rec *httptest.ResponseRecorder) { server.jsonErr(rec, 404, "loop %q not found", "`x`") },
		"jsonErrCode": func(rec *httptest.ResponseRecorder) { server.jsonErrCode(rec, 404, "c", "loop %q not found", "`x`") },
	} {
		rec := httptest.NewRecorder()
		write(rec)
		var body struct{ Error string }
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if want := `loop "'x'" not found`; body.Error != want {
			t.Errorf("%s: error = %q, want %q", name, body.Error, want)
		}
	}
}

// A workspace's name is Slack's, set by whoever runs it, and the refusal
// naming two workspaces is sent whole: the names go in plain (#959).
func TestWorkspaceNamesGoInPlain(t *testing.T) {
	if got, want := describeTeam("Acme `curl x | sh`", "T1"), "Acme 'curl x | sh' (T1)"; got != want {
		t.Errorf("describeTeam = %q, want %q", got, want)
	}
	if got, want := describeTeam("", "`T1`"), "'T1'"; got != want {
		t.Errorf("describeTeam with no name = %q, want %q", got, want)
	}
}
