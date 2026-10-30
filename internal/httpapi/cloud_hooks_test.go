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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/enes-alatas/spool/internal/runtime"
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
	// refuseSession is what MCPSession answers.
	refuseSession error
	// exports is what a SessionStart post is answered with.
	exports []byte
	// posted is each loop SessionPosted was told of.
	posted []string
	// refusedTokens is each session TokenRefused was told of.
	refusedTokens []string
	// checkStage and checkDetail are what CheckSetup answers.
	checkStage, checkDetail string
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

func (hooks *recordingHooks) Stop(_ context.Context, loopID, sessionID string, hook, lines []byte, overBound bool) error {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "Stop", loopID: loopID, session: sessionID, hook: string(hook), lines: string(lines), overBound: overBound})
	return hooks.refuse
}

func (hooks *recordingHooks) SessionPosted(loopID string) {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.posted = append(hooks.posted, loopID)
}

func (hooks *recordingHooks) MCPCall(_ context.Context, loopID, sessionID string) error {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "mcp", loopID: loopID, session: sessionID})
	return hooks.refuse
}

func (hooks *recordingHooks) MCPSession(_ context.Context, loopID, sessionID string) error {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "mcp session", loopID: loopID, session: sessionID})
	return hooks.refuseSession
}

func (hooks *recordingHooks) TokenRefused(sessionID string) {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.refusedTokens = append(hooks.refusedTokens, sessionID)
}

func (hooks *recordingHooks) CheckSetup(_ context.Context, loopID string, source runtime.CloudStart, message string) (string, string) {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.calls = append(hooks.calls, hookCall{event: "check", loopID: loopID, hook: source.Repo + "@" + source.Branch + " in " + source.EnvironmentID})
	return hooks.checkStage, hooks.checkDetail
}

func (hooks *recordingHooks) SetupScript(mcpURL string) (string, error) {
	return "#!/bin/sh\n# setup for " + mcpURL + "\n", nil
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
		{ID: "l-cloud", Name: "cirrus", Runtime: store.RuntimeCloud, HubMCPToken: cloudLoopToken,
			CloudRepo: "example-org/cirrus", CloudBranch: "main", CloudRepos: []string{"example-org/second"}},
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
	body, contentType = hookForm(t, [2]string{"hook", stopInput}, [2]string{"session", "cse_01Synthetic"}, [2]string{"lines", "{\"type\":\"user\"}\n"})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusNoContent {
		t.Fatalf("Stop: %d, want 204", code)
	}

	calls := hooks.taken()
	want := []hookCall{
		{event: "SessionStart", loopID: "l-cloud", session: "cse_01Synthetic", hook: startInput},
		{event: "Stop", loopID: "l-cloud", session: "cse_01Synthetic", hook: stopInput, lines: "{\"type\":\"user\"}\n"},
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

// A SessionStart post naming a session came from it, so it reached the
// hub, though the route refuses it: the runtime is told before any
// refusal, as no verdict that the session can't reach the hub (#1093).
// One naming none, the local CLI's, or on a token that isn't a cloud
// loop's, is not.
func TestCloudHookRouteTellsTheRuntimeOfASessionsRefusedPost(t *testing.T) {
	for name, tc := range map[string]struct {
		token, session  string
		want            int
		posted, refused bool
	}{
		"a session id of another shape": {cloudLoopToken, "cse_not-a-known-shape", http.StatusBadRequest, true, false},
		"no session id":                 {cloudLoopToken, "", http.StatusBadRequest, false, false},
		"a contained loop's token":      {containedLoopToken, "cse_01Synthetic", http.StatusUnauthorized, false, false},
		"an unknown token":              {"spool-hub-synthetic-none", "cse_01Synthetic", http.StatusUnauthorized, false, true},
		"no token":                      {"", "cse_01Synthetic", http.StatusUnauthorized, false, true},
		"no token, no session's shape":  {"", "cse_not-a-known-shape", http.StatusUnauthorized, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			hooks := &recordingHooks{}
			body, contentType := hookForm(t, [2]string{"hook", startInput}, [2]string{"session", tc.session})
			if code := postHook(hookServer(t, hooks), tc.token, body, contentType); code != tc.want {
				t.Fatalf("the post: %d, want %d", code, tc.want)
			}
			hooks.mu.Lock()
			defer hooks.mu.Unlock()
			if told := len(hooks.posted) == 1 && hooks.posted[0] == "l-cloud"; told != tc.posted || len(hooks.posted) > 1 {
				t.Errorf("SessionPosted was told %q, want it told of the cloud loop: %v", hooks.posted, tc.posted)
			}
			// a session refused for its token is named to a setup check (#1182)
			if told := slices.Equal(hooks.refusedTokens, []string{"cse_01Synthetic"}); told != tc.refused || (!tc.refused && len(hooks.refusedTokens) > 0) {
				t.Errorf("TokenRefused was told %q, want the session named: %v", hooks.refusedTokens, tc.refused)
			}
		})
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

// The cloud listener's /mcp asks the runtime about the session a request
// names in its header, and refuses one that names none, or a session the
// runtime refuses, as it refuses an unknown token. The loop's own session
// gets the handshake and the tool list at any time, and outside a turn
// each tool call is refused as a tool error naming why: a 401 between
// turns would leave the session's client failed into the next (ADR-0061).
func TestCloudMCPTakesOnlyTheSessionTheRuntimeAdmits(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)
	call := func(session, body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+cloudLoopToken)
		if session != "" {
			req.Header.Set(cloudSessionHeader, session)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Code, recorder.Body.String()
	}
	const (
		initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
		listTools  = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
		send       = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_message","arguments":{"destination":"control_room","text":"hello"}}}`
	)
	if code, _ := call("cse_01Synthetic", initialize); code != http.StatusOK {
		t.Errorf("a call the runtime admits: %d, want 200", code)
	}
	if code, _ := call("", initialize); code != http.StatusUnauthorized {
		t.Errorf("a call naming no session: %d, want 401", code)
	}
	if code, _ := call("not-a-session", initialize); code != http.StatusUnauthorized {
		t.Errorf("a call naming no session's shape: %d, want 401", code)
	}

	hooks.refuse = errors.New("no turn of this loop is in flight")
	for _, body := range []string{initialize, listTools} {
		if code, answer := call("cse_01Synthetic", body); code != http.StatusOK || strings.Contains(answer, `"error"`) {
			t.Errorf("the loop's session's %s between turns: %d %s, want it answered", body[:40], code, answer)
		}
	}
	if code, answer := call("cse_01Synthetic", send); code != http.StatusOK || !strings.Contains(answer, `"isError":true`) ||
		!strings.Contains(answer, "no turn of this loop is in flight") {
		t.Errorf("the loop's session's send between turns: %d %s, want a tool error naming the turn", code, answer)
	}

	hooks.refuseSession = errors.New("the request isn't from the loop's session")
	if code, _ := call("cse_01Synthetic", initialize); code != http.StatusUnauthorized {
		t.Errorf("a session the runtime refuses: %d, want 401", code)
	}
	session := hookCall{event: "mcp session", loopID: "l-cloud", session: "cse_01Synthetic"}
	turn := hookCall{event: "mcp", loopID: "l-cloud", session: "cse_01Synthetic"}
	want := []hookCall{session, turn, session, turn, session, turn, session, turn, session}
	if calls := hooks.taken(); !slices.Equal(calls, want) {
		t.Errorf("the runtime was asked %+v, want %+v", calls, want)
	}
}

// The add_repo guard's post goes ahead only for one of the loop's repos,
// in any case, from the session the runtime admits (ADR-0061).
func TestCloudHookRouteHoldsAddRepoToTheLoopsRepos(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)
	guard := func(toolInput, session string) int {
		body, contentType := hookForm(t,
			[2]string{"hook", `{"hook_event_name":"PreToolUse","tool_name":"mcp__claude-code-remote__add_repo","tool_input":` + toolInput + `}`},
			[2]string{"session", session})
		return postHook(handler, cloudLoopToken, body, contentType)
	}
	for _, input := range []string{
		`{"owner":"example-org","repo":"second","access":"push"}`,
		`{"owner":"Example-Org","repo":"Cirrus"}`,
		`{"repo":"example-org/second"}`,
	} {
		if code := guard(input, "cse_01Synthetic"); code != http.StatusNoContent {
			t.Errorf("add_repo %s: %d, want 204", input, code)
		}
	}
	for _, input := range []string{
		`{"owner":"example-org","repo":"third"}`,
		`{"owner":"other-org","repo":"second"}`,
		`{"repo":"second"}`,
		`{"owner":"example-org","repo":"second/extra"}`,
		`"example-org/second"`,
	} {
		if code := guard(input, "cse_01Synthetic"); code != http.StatusConflict {
			t.Errorf("add_repo %s: %d, want 409", input, code)
		}
	}
	if code := guard(`{"owner":"example-org","repo":"second"}`, ""); code != http.StatusBadRequest {
		t.Errorf("add_repo naming no session: %d, want 400", code)
	}
	hooks.refuse = errors.New("cloud: the request isn't from the loop's session")
	if code := guard(`{"owner":"example-org","repo":"second"}`, "cse_01ByHand"); code != http.StatusConflict {
		t.Errorf("add_repo from a session the runtime refuses: %d, want 409", code)
	}
}

// A request the cloud listener refuses for its token is logged, at most
// once a minute, with the path and what came but never the token; the
// loop listener's refusals log nothing.
func TestCloudListenerLogsATokenRefusalOnceAMinute(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var logged bytes.Buffer
	server := &Server{Store: db, Log: slog.New(slog.NewTextHandler(&logged, nil))}
	cloudHandler := server.CloudHandler(noRedaction)
	post := func(handler http.Handler, token string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Code
	}
	if code := post(cloudHandler, "spool-hub-synthetic-unknown"); code != http.StatusUnauthorized {
		t.Fatalf("an unknown token: %d, want 401", code)
	}
	if code := post(cloudHandler, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", code)
	}
	if got := strings.Count(logged.String(), "cloud listener refused a request"); got != 1 {
		t.Errorf("two refusals in a minute logged %d lines, want 1:\n%s", got, logged.String())
	}
	if !strings.Contains(logged.String(), "path=/mcp") || !strings.Contains(logged.String(), `came="a token no loop holds"`) ||
		strings.Contains(logged.String(), "synthetic-unknown") {
		t.Errorf("the line doesn't name the path and what came, or names the token:\n%s", logged.String())
	}

	server.cloudRefusedLogged.Store(0)
	logged.Reset()
	if code := post(server.mcpHandler(everyLoop), ""); code != http.StatusUnauthorized {
		t.Fatalf("no token on the loop listener: %d, want 401", code)
	}
	if logged.Len() != 0 {
		t.Errorf("the loop listener's refusal was logged:\n%s", logged.String())
	}
}

// An MCP request refused for its token names its session to a setup check,
// as a refused hook post does: the session's client connects before its
// SessionStart post, so it may be the first word that the session reached
// the hub with the wrong token (#1182).
func TestCloudMCPTellsTheRuntimeOfASessionRefusedForItsToken(t *testing.T) {
	hooks := &recordingHooks{}
	handler := hookServer(t, hooks)
	for _, session := range []string{"cse_01Synthetic", "not-a-session"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer spool-hub-synthetic-none")
		req.Header.Set("X-Spool-Session", session)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("an unknown token: %d, want 401", recorder.Code)
		}
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if !slices.Equal(hooks.refusedTokens, []string{"cse_01Synthetic"}) {
		t.Errorf("TokenRefused was told %q, want the one session of a session's shape", hooks.refusedTokens)
	}
}
