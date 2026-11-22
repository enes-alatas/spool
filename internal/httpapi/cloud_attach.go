package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/route"
)

// cloudAttachPath is the route a cloud session's send_message hook posts
// the file the call attaches to, ahead of the call (ADR-0073). It is the
// cloud runtime's AttachPath.
const cloudAttachPath = "/hooks/attach"

// Bounds on an attachment's post once its headers have passed. Until then
// it has the cloud listener's, as every request has.
const (
	// cloudMaxAttachBody is the largest file and room for the form's
	// other parts.
	cloudMaxAttachBody = attach.MaxSize + 64<<10
	// cloudAttachDeadline is how long the post may take to come in and be
	// answered: 20 MB over a slow tunnel.
	cloudAttachDeadline = 2*time.Minute + 30*time.Second
	// cloudMaxAttachName bounds the file's name part.
	cloudMaxAttachName = 4 << 10
)

// sendMessageHookTool is the name the CLI gives the hub's send_message in
// a hook's input: the spool server's tool.
const sendMessageHookTool = "mcp__spool__send_message"

// handleCloudAttach takes the file a cloud loop's session posts for one
// send_message call, and keeps it until that call claims it (ADR-0073).
// It decides from the headers alone, in this order, before a byte of the
// body is read: the token, the session's shape, that the session is the
// loop's own and a turn of it runs, and that no other file of the loop is
// on its way in. Only then does the post get its own bound and deadline,
// on raw, the body under the cloud listener's bound. The form is the
// hook's input, which must be send_message's PreToolUse naming its call,
// the file's name, and the file. 204 takes it; any other answer refuses
// the call, and its text is the reason the model reads. w is the
// redacting writer, unredacted the connection's own, for its deadlines.
func (server *Server) handleCloudAttach(w, unredacted http.ResponseWriter, r *http.Request, raw io.ReadCloser) {
	if r.Method != http.MethodPost || server.CloudHooks == nil || server.Router == nil {
		http.NotFound(w, r)
		return
	}
	caller, ok := server.mcpCaller(w, r)
	if !ok {
		return
	}
	if !runningCloudLoop(caller) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	refuse := func(status int, reason string) {
		server.Log.Warn("cloud attachment refused", "loop", caller.Name, "status", status, "reason", reason)
		http.Error(w, reason, status)
	}
	session := r.Header.Get(cloudSessionHeader)
	if !cloudSessionID.MatchString(session) {
		refuse(http.StatusBadRequest, "no session id")
		return
	}
	if err := server.CloudHooks.MCPCall(r.Context(), caller.ID, session); err != nil {
		refuse(http.StatusConflict, err.Error())
		return
	}
	if _, busy := server.cloudAttaching.LoadOrStore(caller.ID, struct{}{}); busy {
		refuse(http.StatusConflict, "another of your files is on its way to the hub; send it first")
		return
	}
	defer server.cloudAttaching.Delete(caller.ID)

	deadline := time.Now().Add(cloudAttachDeadline)
	connection := http.NewResponseController(unredacted)
	if err := connection.SetReadDeadline(deadline); err != nil {
		server.Log.Warn("cloud attachment's read deadline not raised", "loop", caller.Name, "err", err)
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		server.Log.Warn("cloud attachment's write deadline not raised", "loop", caller.Name, "err", err)
	}
	r.Body = http.MaxBytesReader(unredacted, raw, cloudMaxAttachBody)

	post, err := readAttachPost(r)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		refuse(http.StatusRequestEntityTooLarge, "the file is over the 20 MB limit")
		return
	case err != nil:
		refuse(http.StatusBadRequest, "the post can't be read")
		return
	}
	var hook struct {
		Event     string `json:"hook_event_name"`
		Tool      string `json:"tool_name"`
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(post.hook, &hook) != nil || hook.Event != "PreToolUse" || hook.Tool != sendMessageHookTool || hook.ToolUseID == "" {
		refuse(http.StatusBadRequest, "the post names no send_message call")
		return
	}
	call := route.CloudCall{Session: session, ToolUseID: hook.ToolUseID}
	serr, err := server.Router.KeepCloudUpload(r.Context(), caller, call, post.name, post.file)
	if err != nil {
		server.Log.Error("cloud attachment not kept", "loop", caller.Name, "err", err)
		http.Error(w, "the hub couldn't keep the file; try again", http.StatusInternalServerError)
		return
	}
	if serr != nil {
		refuse(http.StatusConflict, serr.Detail)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// attachPost is an attachment's post as handleCloudAttach reads it.
type attachPost struct {
	hook, file []byte
	name       string
}

// readAttachPost reads an attachment's parts into memory, never a
// temporary file: holdsSecret reads the whole text. A part over its bound
// fails with the *http.MaxBytesError; one it doesn't know is read under
// cloudMaxHookInput and dropped, as readHookPost does.
func readAttachPost(r *http.Request) (attachPost, error) {
	var post attachPost
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "multipart/form-data" {
		return post, errors.New("not a multipart form")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return post, err
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return post, nil
		}
		if err != nil {
			return post, err
		}
		switch part.FormName() {
		case "hook":
			post.hook, err = readPart(part, cloudMaxHookInput)
		case "name":
			var name []byte
			name, err = readPart(part, cloudMaxAttachName)
			post.name = string(name)
		case "file":
			post.file, err = readPart(part, attach.MaxSize)
		default:
			_, err = readPart(part, cloudMaxHookInput)
		}
		if err != nil {
			return post, err
		}
	}
}
