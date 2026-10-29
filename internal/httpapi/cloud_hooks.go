package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
)

// cloudHooksPath is the route a cloud session's hooks post to on the cloud
// listener, beside /mcp (ADR-0059). It is the cloud runtime's HooksPath.
const cloudHooksPath = "/hooks"

// cloudMaxHookBody bounds one hook post: a turn's new transcript lines,
// which carry each tool result whole. It is applied before the token is
// read, as every cloud listener bound is.
const cloudMaxHookBody = 16 << 20

// cloudMaxHookInput bounds the hook's own input and the session id, read
// into memory before the lines.
const cloudMaxHookInput = 64 << 10

// cloudSessionID is the shape of a cloud session's id, the one thing a
// SessionStart post binds: cse_… as the VM's CLAUDE_CODE_REMOTE_SESSION_ID
// names it, or session_… as claude.ai does (#1099). Anything else is
// refused before it reaches the runtime.
var cloudSessionID = regexp.MustCompile(`^(?:cse|session)_[A-Za-z0-9]{1,128}$`)

// CloudHooks takes the posts of a cloud loop's hooks (ADR-0059). Each
// call names the loop whose token the post carried; an error refuses the
// post. The posts are the loop's own word: the runtime takes from a Stop
// post only its turn's read-back.
type CloudHooks interface {
	// SessionStart answers a SessionStart post with the loop's connections
	// as export lines at a start of that loop, recording sessionID as its
	// session.
	SessionStart(ctx context.Context, loopID, sessionID string, hook []byte) ([]byte, error)
	// Stop ends the loop's turn in flight with what the post carries.
	// overBound says the post ran past the route's bound, and lines holds
	// none of it: the turn ends as an error.
	Stop(ctx context.Context, loopID string, hook, lines []byte, overBound bool) error
	// SessionPosted is told of each SessionStart post of the loop that
	// names a session, before any refusal: the session reached the hub.
	SessionPosted(loopID string)
}

// handleCloudHook serves a cloud session's hook post: a multipart form with
// the hook's input as its hook field, the session id for SessionStart, and
// the transcript's new lines for Stop. The parts are read into memory,
// never a temporary file: the lines carry the loop's turn. 204 takes a
// Stop post, 200 answers a SessionStart one with the loop's connections
// and prompt, and 409 refuses either. w is the redacting writer every
// response goes through; handOff is the one written past it, the
// SessionStart answer, whose values must arrive as they are (ADR-0059).
func (server *Server) handleCloudHook(w, handOff http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || server.CloudHooks == nil {
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
	// a refused post is logged, as the loop that sent it can't be told
	refuse := func(status int, event, reason string) {
		server.Log.Warn("cloud hook refused", "loop", caller.Name, "event", event, "status", status, "reason", reason)
		http.Error(w, reason, status)
	}
	post, err := readHookPost(r)
	// A SessionStart post naming a session came from its VM, which reached
	// the hub, though it may be refused below. The local CLI's own post
	// names none and comes from this machine, so it never counts (#1093).
	if post.event == "SessionStart" && post.session != "" {
		server.CloudHooks.SessionPosted(caller.ID)
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge) && post.event == "Stop":
		_ = server.CloudHooks.Stop(r.Context(), caller.ID, post.hook, nil, true)
		refuse(http.StatusRequestEntityTooLarge, post.event, "the post is over its bound")
		return
	case errors.As(err, &tooLarge):
		refuse(http.StatusRequestEntityTooLarge, post.event, "the post is over its bound")
		return
	case err != nil:
		refuse(http.StatusBadRequest, post.event, "the post can't be read")
		return
	}
	switch post.event {
	case "SessionStart":
		if !cloudSessionID.MatchString(post.session) {
			refuse(http.StatusBadRequest, post.event, "no session id")
			return
		}
		// written past the redactor on purpose: the values are the answer
		var exports []byte
		if exports, err = server.CloudHooks.SessionStart(r.Context(), caller.ID, post.session, post.hook); err == nil {
			handOff.Header().Set("Content-Type", "text/plain; charset=utf-8")
			handOff.Header().Set("Cache-Control", "no-store")
			handOff.WriteHeader(http.StatusOK)
			_, _ = handOff.Write(exports)
			return
		}
	case "Stop":
		err = server.CloudHooks.Stop(r.Context(), caller.ID, post.hook, post.lines, false)
	default:
		refuse(http.StatusBadRequest, post.event, "no such hook")
		return
	}
	if err != nil {
		refuse(http.StatusConflict, post.event, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hookPost is a hook post as handleCloudHook reads it. event is the
// hook's hook_event_name, read once the hook field is in.
type hookPost struct {
	event, session string
	hook, lines    []byte
}

// readHookPost reads a hook post's parts. A part over its bound fails with
// the *http.MaxBytesError, and whatever was read before it is kept, so an
// over-bound Stop post is still known as one.
func readHookPost(r *http.Request) (hookPost, error) {
	var post hookPost
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
			if post.hook, err = readPart(part, cloudMaxHookInput); err != nil {
				return post, err
			}
			var input struct {
				Event string `json:"hook_event_name"`
			}
			if json.Unmarshal(post.hook, &input) != nil {
				return post, errors.New("the hook's input can't be read")
			}
			post.event = input.Event
		case "session":
			session, err := readPart(part, cloudMaxHookInput)
			if err != nil {
				return post, err
			}
			post.session = string(session)
		case "lines":
			if post.lines, err = readPart(part, cloudMaxHookBody); err != nil {
				return post, err
			}
		default:
			return post, errors.New("an unknown part")
		}
	}
}

// readPart reads one part whole, up to limit bytes; past it, the error is
// a *http.MaxBytesError.
func readPart(part io.Reader, limit int64) ([]byte, error) {
	read, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(read)) > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
	return read, nil
}
