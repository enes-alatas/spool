package httpapi

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// Attachments in the control room (#460): every message a route serves
// carries the files it came with, a file is served by its id, and the
// composer uploads one to send with its next message.

const (
	codeAttachmentNotFound     = "attachment_not_found"
	codeAttachmentGone         = "attachment_gone"
	codeAttachmentTooLarge     = route.ErrAttachmentTooLarge
	codeAttachmentNameRequired = "attachment_name_required"
	codeAttachmentEmpty        = "attachment_empty"
	codeAttachmentUnavailable  = route.ErrAttachmentUnavailable
)

// uploadPath is the one route whose body is not JSON (ADR-0030, amended).
const uploadPath = "/api/attachments"

// messageView is a message as the control room reads it: the stored row
// and the files it carries.
type messageView struct {
	*store.Message
	Attachments []*store.Attachment `json:"attachments,omitempty"`
}

// messageViews gives each message its attachments, in one query.
func (server *Server) messageViews(ctx context.Context, msgs []*store.Message) ([]messageView, error) {
	ids := make([]int64, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.ID
	}
	byMessage, err := server.Store.Attachments().ByMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	views := make([]messageView, len(msgs))
	for i, msg := range msgs {
		views[i] = messageView{Message: msg, Attachments: byMessage[msg.ID]}
	}
	return views, nil
}

// writeMessages answers with msgs as the control room reads them.
func (server *Server) writeMessages(w http.ResponseWriter, r *http.Request, msgs []*store.Message) {
	views, err := server.messageViews(r.Context(), msgs)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

// inlineImages are the types served for the page to show. Anything else,
// SVG and HTML above all, is a download: a stranger's file must not run on
// the hub's origin.
var inlineImages = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// handleAttachment serves an attachment's bytes, behind the same guard as
// every route, so the session cookie lets an <img> load one.
func (server *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		server.jsonErrCode(w, 404, codeAttachmentNotFound, "no attachment %q", r.PathValue("id"))
		return
	}
	row, hostPath, err := server.Router.AttachmentFile(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeAttachmentNotFound, "no attachment %d", id)
		return
	case errors.Is(err, route.ErrAttachmentGone):
		server.jsonErrCode(w, 410, codeAttachmentGone, "attachment %d is no longer kept", id)
		return
	case err != nil:
		server.jsonErr(w, 500, "%v", err)
		return
	}
	file, err := os.Open(hostPath)
	if errors.Is(err, os.ErrNotExist) {
		server.jsonErrCode(w, 410, codeAttachmentGone, "attachment %d is no longer kept", id)
		return
	} else if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	defer file.Close()
	header := w.Header()
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "private, max-age=3600")
	disposition := "attachment"
	if inlineImages[row.MIME] && r.URL.Query().Get("download") == "" {
		disposition = "inline"
		header.Set("Content-Type", row.MIME)
	} else {
		header.Set("Content-Type", "application/octet-stream")
		header.Set("Content-Security-Policy", "sandbox")
	}
	header.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": row.Name}))
	http.ServeContent(w, r, "", time.UnixMilli(row.CreatedAt), file)
}

// handleUpload keeps a file the composer uploads, raw, to send with the
// operator's next message by its id (#460).
func (server *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		server.jsonErrCode(w, 400, codeAttachmentNameRequired, "name the file: ?name=")
		return
	}
	// The guard lets JSON through to any route; this one takes only the
	// file, raw (ADR-0030).
	if !isOctetStream(r.Header.Get("Content-Type")) {
		server.jsonErr(w, http.StatusUnsupportedMediaType, "this route takes application/octet-stream")
		return
	}
	if r.ContentLength > attach.MaxSize {
		server.jsonErrCode(w, 413, codeAttachmentTooLarge, "the file is over the %d MB limit", attach.MaxSize>>20)
		return
	}
	body := bufio.NewReader(http.MaxBytesReader(w, r.Body, attach.MaxSize+1))
	if _, err := body.Peek(1); errors.Is(err, io.EOF) {
		server.jsonErrCode(w, 400, codeAttachmentEmpty, "the file is empty")
		return
	}
	kept, err := server.Router.KeepUpload(r.Context(), body, name)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, attach.ErrTooLarge), errors.As(err, &tooLarge):
		server.jsonErrCode(w, 413, codeAttachmentTooLarge, "the file is over the %d MB limit", attach.MaxSize>>20)
		return
	case errors.Is(err, route.ErrNoFiles):
		server.jsonErrCode(w, 409, codeAttachmentUnavailable, "%v", err)
		return
	case err != nil:
		server.jsonErr(w, 500, "%v", err)
		return
	}
	_, _ = io.Copy(io.Discard, body)
	writeJSON(w, 201, kept)
}

// ingestErr answers a composer message the router refused.
func (server *Server) ingestErr(w http.ResponseWriter, err error) {
	if errors.Is(err, route.ErrUploadNotFound) {
		server.jsonErrCode(w, 404, codeAttachmentNotFound, "%v; upload the file again", err)
		return
	}
	server.jsonErr(w, 500, "%v", err)
}
