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
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime/cloud"
	"github.com/enes-alatas/spool/internal/store"
)

// attachServer is hookServer's hub with a router that keeps files, and
// the server itself, for its in-flight slots.
func attachServer(t *testing.T, hooks CloudHooks) (http.Handler, *Server, store.Store) {
	t.Helper()
	_, db := hookServerStore(t, hooks, noRedaction)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := route.New(db, nil, nil, log)
	files, err := attach.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router.SetFiles(files, nil)
	server := &Server{Store: db, Log: log, CloudHooks: hooks, Router: router}
	return server.CloudHandler(noRedaction), server, db
}

const sendHookInput = `{"session_id":"4f1c-synthetic","hook_event_name":"PreToolUse","tool_name":"mcp__spool__send_message",` +
	`"tool_use_id":"toolu_01Synthetic","tool_input":{"destination":"owner_dm","text":"the file","attach":"report.txt"}}`

// attachForm is send_message's hook post as curl -F sends it.
func attachForm(t *testing.T, hook, name string, file []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("hook", hook); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("name", name); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, form.FormDataContentType()
}

// countingReader counts what is read of a body.
type countingReader struct {
	inner io.Reader
	read  int
}

func (counting *countingReader) Read(buf []byte) (int, error) {
	read, err := counting.inner.Read(buf)
	counting.read += read
	return read, err
}

func postAttach(handler http.Handler, token, session string, body io.Reader, contentType string) (int, string) {
	req := httptest.NewRequest(http.MethodPost, cloudAttachPath, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		req.Header.Set(cloudSessionHeader, session)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

func TestCloudAttachPathIsTheRuntimes(t *testing.T) {
	if cloudAttachPath != cloud.AttachPath || sendMessageHookTool != cloud.SendMessageTool {
		t.Errorf("the route is %s for %s, the runtime's hook posts to %s for %s",
			cloudAttachPath, sendMessageHookTool, cloud.AttachPath, cloud.SendMessageTool)
	}
}

// The route decides from the headers alone before it reads a byte of the
// body: a post with no token, another runtime's token, no session, a
// session that isn't the loop's own in a turn, or while another of the
// loop's files is on its way in is refused, and no more than the cloud
// listener's 1 MB bound is ever read of it (ADR-0073).
func TestCloudAttachDecidesFromTheHeadersFirst(t *testing.T) {
	file := bytes.Repeat([]byte("x"), 3<<20)
	for name, tc := range map[string]struct {
		token, session string
		refuse         error
		busy           bool
		want           int
	}{
		"no token":               {"", "cse_01Synthetic", nil, false, http.StatusUnauthorized},
		"a contained loop":       {containedLoopToken, "cse_01Synthetic", nil, false, http.StatusUnauthorized},
		"no session":             {cloudLoopToken, "", nil, false, http.StatusBadRequest},
		"not the loop's in turn": {cloudLoopToken, "cse_01Synthetic", errors.New("no turn of this session runs"), false, http.StatusConflict},
		"one already on its way": {cloudLoopToken, "cse_01Synthetic", nil, true, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			hooks := &recordingHooks{refuse: tc.refuse}
			handler, server, db := attachServer(t, hooks)
			if tc.busy {
				server.cloudAttaching.Store("l-cloud", struct{}{})
			}
			body, contentType := attachForm(t, sendHookInput, "report.txt", file)
			counting := &countingReader{inner: body}
			code, _ := postAttach(handler, tc.token, tc.session, counting, contentType)
			if code != tc.want {
				t.Errorf("answered %d, want %d", code, tc.want)
			}
			if counting.read > cloudMaxBody {
				t.Errorf("read %d bytes of a refused post, past the listener's %d", counting.read, cloudMaxBody)
			}
			if row, err := db.Attachments().Get(context.Background(), 1); err == nil {
				t.Errorf("a refused post was kept: %+v", row)
			}
		})
	}
}

// A post that passes gets its own bound: a file over the cloud listener's
// 1 MB is kept, waiting for its send, and the loop's slot is free again.
// One over 20 MB, or naming no send_message call, is refused and nothing
// is kept.
func TestCloudAttachKeepsTheFileOfItsLoopsCall(t *testing.T) {
	hooks := &recordingHooks{}
	handler, server, db := attachServer(t, hooks)
	file := bytes.Repeat([]byte("report "), 300<<10) // 2.1 MB
	body, contentType := attachForm(t, sendHookInput, "report.txt", file)
	if code, reason := postAttach(handler, cloudLoopToken, "cse_01Synthetic", body, contentType); code != http.StatusNoContent {
		t.Fatalf("answered %d %q, want 204", code, reason)
	}
	row, err := db.Attachments().Get(context.Background(), 1)
	if err != nil || row.Name != "report.txt" || row.Size != int64(len(file)) || row.MessageID != 0 {
		t.Errorf("kept %+v, %v; want the file, waiting for its send", row, err)
	}
	if calls := hooks.taken(); len(calls) != 1 || calls[0].event != "mcp" || calls[0].session != "cse_01Synthetic" {
		t.Errorf("CloudHooks was asked %+v, want MCPCall for the header's session", calls)
	}
	if _, busy := server.cloudAttaching.Load("l-cloud"); busy {
		t.Error("the loop's slot is still taken")
	}

	for name, post := range map[string]struct {
		hook string
		file []byte
		want int
	}{
		"over 20 MB":         {sendHookInput, make([]byte, attach.MaxSize+1), http.StatusRequestEntityTooLarge},
		"another tool":       {strings.Replace(sendHookInput, "mcp__spool__send_message", "mcp__claude-code-remote__add_repo", 1), []byte("x"), http.StatusBadRequest},
		"no call id":         {strings.Replace(sendHookInput, `"tool_use_id":"toolu_01Synthetic",`, "", 1), []byte("x"), http.StatusBadRequest},
		"another hook event": {strings.Replace(sendHookInput, "PreToolUse", "PostToolUse", 1), []byte("x"), http.StatusBadRequest},
	} {
		body, contentType := attachForm(t, post.hook, "other.txt", post.file)
		if code, _ := postAttach(handler, cloudLoopToken, "cse_01Synthetic", body, contentType); code != post.want {
			t.Errorf("%s: answered %d, want %d", name, code, post.want)
		}
	}
	if _, err := db.Attachments().Get(context.Background(), 2); err == nil {
		t.Error("a refused post was kept")
	}
	if _, busy := server.cloudAttaching.Load("l-cloud"); busy {
		t.Error("a refused post left the loop's slot taken")
	}
}

// The loop's Stop removes a file its send never came for.
func TestCloudAttachUnclaimedGoesAtStop(t *testing.T) {
	hooks := &recordingHooks{}
	handler, _, db := attachServer(t, hooks)
	body, contentType := attachForm(t, sendHookInput, "report.txt", []byte("the report"))
	if code, reason := postAttach(handler, cloudLoopToken, "cse_01Synthetic", body, contentType); code != http.StatusNoContent {
		t.Fatalf("answered %d %q, want 204", code, reason)
	}
	body, contentType = hookForm(t, [2]string{"hook", stopInput}, [2]string{"session", "cse_01Synthetic"}, [2]string{"lines", ""})
	if code := postHook(handler, cloudLoopToken, body, contentType); code != http.StatusNoContent {
		t.Fatalf("Stop: %d, want 204", code)
	}
	if row, err := db.Attachments().Get(context.Background(), 1); err != nil || row.RemovedAt == 0 {
		t.Errorf("after Stop the upload is %+v, %v; want removed", row, err)
	}
}
