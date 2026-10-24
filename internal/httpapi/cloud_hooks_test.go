package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// Synthetic hub tokens, never real ones.
const (
	cloudLoopToken     = "spool-hub-synthetic-cloud"
	containedLoopToken = "spool-hub-synthetic-docker"
)

// hookCall is one call the route made into CloudHooks.
type hookCall struct {
	event, loopID, session string
	hook, lines            string
	overBound              bool
}

// recordingHooks keeps every call, and refuses each with refuse when set.
type recordingHooks struct {
	mu     sync.Mutex
	calls  []hookCall
	refuse error
	// exports is what a SessionStart post is answered with.
	exports []byte
}

func (hooks *recordingHooks) SessionStart(_ context.Context, loopID, sessionID string, hook []byte) ([]byte, error) {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "SessionStart", loopID: loopID, session: sessionID, hook: string(hook)})
	if hooks.refuse != nil {
		return nil, hooks.refuse
	}
	return hooks.exports, nil
}

func (hooks *recordingHooks) Stop(_ context.Context, loopID string, hook, lines []byte, overBound bool) error {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "Stop", loopID: loopID, hook: string(hook), lines: string(lines), overBound: overBound})
	return hooks.refuse
}

func (hooks *recordingHooks) taken() []hookCall {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return append([]hookCall(nil), hooks.calls...)
}

// noRedaction is a cloud listener's redactor for a test with no secret to
// redact.
func noRedaction(next http.Handler) http.Handler { return next }

// hookServer is a hub with one cloud loop and one contained loop, whose
// cloud listener hands hook posts to hooks.
func hookServer(t *testing.T, hooks CloudHooks) http.Handler {
	t.Helper()
	return hookServerRedacting(t, hooks, noRedaction)
}

// hookServerRedacting is hookServer whose cloud listener redacts through
// redacted.
func hookServerRedacting(t *testing.T, hooks CloudHooks, redacted func(http.Handler) http.Handler) http.Handler {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, loopRecord := range []store.Loop{
		{ID: "l-cloud", Name: "cirrus", Runtime: store.RuntimeCloud, HubMCPToken: cloudLoopToken},
		{ID: "l-docker", Name: "aster", Runtime: store.RuntimeDocker, HubMCPToken: containedLoopToken},
	} {
		loopRecord.Status, loopRecord.WorkspaceMode, loopRecord.Pacing = store.StatusActive, store.WorkspaceNone, store.PacingFixed
		loopRecord.CreatedAt, loopRecord.UpdatedAt = 1, 1
		if err := db.Loops().Create(context.Background(), &loopRecord); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CloudHooks: hooks}
	return server.CloudHandler(redacted)
}

// hookForm is a hook post's body as curl -F sends it, and its content type.
func hookForm(t *testing.T, parts ...[2]string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, part := range parts {
		if err := form.WriteField(part[0], part[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}

func postHook(handler http.Handler, token string, body io.Reader, contentType string) int {
	req := httptest.NewRequest(http.MethodPost, cloudHooksPath, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder.Code
}

const (
	startInput = `{"hook_event_name":"SessionStart","source":"startup"}`
	stopInput  = `{"hook_event_name":"Stop","last_assistant_message":"pong"}`
)

func TestCloudHookRouteHandsEachPostToItsLoop(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)

	body, contentType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "cse_01Synthetic"})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusOK {
		t.Fatalf("SessionStart: %d, want 200", code)
	}
	body, contentType = hookForm(t, [2]string{"hook", stopInput}, [2]string{"lines", "{\"type\":\"user\"}\n"})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusNoContent {
		t.Fatalf("Stop: %d, want 204", code)
	}

	calls := hooks.taken()
	want := []hookCall{
		{event: "SessionStart", loopID: "l-cloud", session: "cse_01Synthetic", hook: startInput},
		{event: "Stop", loopID: "l-cloud", hook: stopInput, lines: "{\"type\":\"user\"}\n"},
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("the route made %+v, want %+v", calls, want)
	}
}

func TestCloudHookRouteRefusesWhatIsNotACloudLoopsPost(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)
	start := func(session string) (*bytes.Buffer, string) {
		return hookForm(t, [2]string{"hook", startInput}, [2]string{"session", session})
	}

	for name, tc := range map[string]struct {
		token string
		body  func() (*bytes.Buffer, string)
		want  int
	}{
		"no token":                      {"", func() (*bytes.Buffer, string) { return start("session_01Synthetic") }, http.StatusUnauthorized},
		"an unknown token":              {"spool-hub-synthetic-none", func() (*bytes.Buffer, string) { return start("session_01Synthetic") }, http.StatusUnauthorized},
		"a contained loop's token":      {containedLoopToken, func() (*bytes.Buffer, string) { return start("session_01Synthetic") }, http.StatusUnauthorized},
		"no session id":                 {cloudLoopToken, func() (*bytes.Buffer, string) { return start("") }, http.StatusBadRequest},
		"a session id of another shape": {cloudLoopToken, func() (*bytes.Buffer, string) { return start("session_../x") }, http.StatusBadRequest},
		"an unknown event": {cloudLoopToken, func() (*bytes.Buffer, string) {
			return hookForm(t, [2]string{"hook", `{"hook_event_name":"PreToolUse"}`})
		}, http.StatusBadRequest},
		"a hook input that isn't JSON": {cloudLoopToken, func() (*bytes.Buffer, string) {
			return hookForm(t, [2]string{"hook", `nope`})
		}, http.StatusBadRequest},
		"an unknown part": {cloudLoopToken, func() (*bytes.Buffer, string) {
			return hookForm(t, [2]string{"hook", stopInput}, [2]string{"result", "forged"})
		}, http.StatusBadRequest},
		"not a form": {cloudLoopToken, func() (*bytes.Buffer, string) {
			return bytes.NewBufferString(stopInput), "application/json"
		}, http.StatusBadRequest},
	} {
		body, contentType := tc.body()
		if code := postHook(handler, tc.token, body, contentType); code != tc.want {
			t.Errorf("%s: %d, want %d", name, code, tc.want)
		}
	}
	if calls := hooks.taken(); len(calls) != 0 {
		t.Errorf("refused posts reached the runtime: %+v", calls)
	}
}

func TestCloudHookRouteAnswersTheRuntimesRefusal(t *testing.T) {
	handler := hookServer(t, &recordingHooks{refuse: errors.New("no start pending")})
	body, contentType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "session_01Synthetic"})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusConflict {
		t.Errorf("a refused SessionStart: %d, want 409", code)
	}
}

// A Stop post over the route's bound still ends its turn, as an error, so
// the turn never waits out the backstop for a post that came.
func TestCloudHookRouteEndsATurnWhosePostIsOverItsBound(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)
	body, contentType := hookForm(t, [2]string{"hook", stopInput}, [2]string{"lines", strings.Repeat("x", cloudMaxHookBody)})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an over-bound Stop: %d, want 413", code)
	}
	calls := hooks.taken()
	if len(calls) != 1 || !calls[0].overBound || calls[0].lines != "" || calls[0].loopID != "l-cloud" {
		t.Errorf("the route made %+v, want one Stop marked over its bound, with no lines", calls)
	}
}

func TestCloudHookRouteIsOffWithNoHooksWired(t *testing.T) {
	handler := hookServer(t, nil)
	body, contentType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "session_01Synthetic"})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusNotFound {
		t.Errorf("with no CloudHooks: %d, want 404", code)
	}
}

// markedWriter is a response writer that marks every response written
// through it, standing in for the hub's redactor.
type markedWriter struct{ http.ResponseWriter }

func (writer markedWriter) WriteHeader(status int) {
	writer.Header().Set("X-Redacted", "yes")
	writer.ResponseWriter.WriteHeader(status)
}

func (writer markedWriter) Write(data []byte) (int, error) {
	writer.Header().Set("X-Redacted", "yes")
	return writer.ResponseWriter.Write(data)
}

// The connections' answer to a SessionStart post is written past the
// redactor, on purpose, and so is nothing else on the cloud listener: a
// refusal, a Stop post's answer, /mcp and an unknown path all go through
// it (ADR-0059).
func TestCloudListenerWritesOnlyTheHandOffPastTheRedactor(t *testing.T) {
	const exports = "export GH_TOKEN='synthetic-value'\n"
	hooks := &recordingHooks{exports: []byte(exports)}
	handler := hookServerRedacting(t, hooks, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(markedWriter{w}, r) })
	})
	serve := func(method, path, token string, body io.Reader, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", contentType)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	body, contentType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "session_01Synthetic"})
	answer := serve(http.MethodPost, cloudHooksPath, cloudLoopToken, body, contentType)
	if answer.Code != http.StatusOK || answer.Body.String() != exports || answer.Header().Get("X-Redacted") != "" {
		t.Errorf("the hand-off is %d %q (redacted %q), want 200 with the exports, past the redactor",
			answer.Code, answer.Body.String(), answer.Header().Get("X-Redacted"))
	}
	if got := answer.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("the hand-off's content type is %q, want text/plain", got)
	}

	stopBody, stopType := hookForm(t, [2]string{"hook", stopInput}, [2]string{"lines", ""})
	refusedBody, refusedType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "not-a-session"})
	for name, response := range map[string]*httptest.ResponseRecorder{
		"a Stop post":          serve(http.MethodPost, cloudHooksPath, cloudLoopToken, stopBody, stopType),
		"a refused post":       serve(http.MethodPost, cloudHooksPath, cloudLoopToken, refusedBody, refusedType),
		"a post with no token": serve(http.MethodPost, cloudHooksPath, "", strings.NewReader(""), "text/plain"),
		"/mcp":                 serve(http.MethodPost, "/mcp", cloudLoopToken, strings.NewReader(`{}`), "application/json"),
		"an unknown path":      serve(http.MethodGet, "/api/loops", cloudLoopToken, nil, ""),
	} {
		if response.Header().Get("X-Redacted") != "yes" {
			t.Errorf("%s (%d) was written past the redactor", name, response.Code)
		}
	}
	hooks.refuse = errors.New("no start pending")
	refused, refusedType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", "session_01Synthetic"})
	if response := serve(http.MethodPost, cloudHooksPath, cloudLoopToken, refused, refusedType); response.Header().Get("X-Redacted") != "yes" ||
		strings.Contains(response.Body.String(), "synthetic-value") {
		t.Errorf("a refused SessionStart post (%d) was written past the redactor, or carried the exports", response.Code)
	}
}
