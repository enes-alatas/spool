package route

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/store"
)

// A cloud loop's files are on Anthropic's VM, which the hub can't read.
// The hook of its session's send_message posts the file it attaches
// first, and the send claims it (ADR-0073).

// CloudCall is the send_message call of a cloud loop's session: the
// session, as the VM names it, and the CLI's id for the call, which its
// PreToolUse hook and its tools/call both carry.
type CloudCall struct {
	Session   string
	ToolUseID string
}

// CloudUploadsWaiting is how many uploads of one loop may wait for their
// sends at once. A send claims its upload right after the hook posts it,
// so more than one waits only for a call the CLI never made.
const CloudUploadsWaiting = 4

type cloudUploadKey struct {
	loopID string
	call   CloudCall
}

// KeepCloudUpload keeps the file a cloud loop's session posted for one
// send_message call, until that call claims it (ADR-0073). It checks what
// keepSent checks of a workstation's file: the name is redacted first,
// the size bound, and that the text holds no secret the hub knows. A
// refusal is a SendError, which the hook hands the model as its reason.
// The same call posting again, as curl does after a lost answer, replaces
// what it posted before.
func (router *Router) KeepCloudUpload(ctx context.Context, from *store.Loop, call CloudCall, name string, body []byte) (*SendError, error) {
	if router.files == nil {
		return &SendError{ErrAttachmentUnavailable, "this hub keeps no files, so a message cannot carry one; send the words alone"}, nil
	}
	if router.redactName != nil {
		name = router.redactName(name)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "file"
	}
	if len(body) > attach.MaxSize {
		return &SendError{ErrAttachmentTooLarge, fmt.Sprintf("%s is over the %d MB limit", name, attach.MaxSize>>20)}, nil
	}
	if router.redactName != nil && router.holdsSecret(string(body)) {
		return &SendError{ErrSecretInAttachment,
			fmt.Sprintf("%s holds a secret this hub knows, and a file is never sent with one; attach a copy without it", name)}, nil
	}
	key := cloudUploadKey{from.ID, call}
	router.cloudMu.Lock()
	waiting := 0
	for other := range router.cloudUploads {
		if other.loopID == from.ID && other != key {
			waiting++
		}
	}
	router.cloudMu.Unlock()
	if waiting >= CloudUploadsWaiting {
		return &SendError{ErrAttachmentUnavailable,
			fmt.Sprintf("%d of your files are already waiting for their sends; send the words alone", waiting)}, nil
	}
	kept, err := router.files.Keep(bytes.NewReader(body), name)
	if errors.Is(err, attach.ErrTooLarge) {
		return &SendError{ErrAttachmentTooLarge, fmt.Sprintf("%s is over the %d MB limit", name, attach.MaxSize>>20)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("keep cloud upload: %w", err)
	}
	kept.CreatedAt = time.Now().UnixMilli()
	if err := router.store.Attachments().Insert(ctx, kept); err != nil {
		router.dropKept(kept)
		return nil, fmt.Errorf("record cloud upload: %w", err)
	}
	router.cloudMu.Lock()
	if router.cloudUploads == nil {
		router.cloudUploads = map[cloudUploadKey]int64{}
	}
	replaced, again := router.cloudUploads[key]
	router.cloudUploads[key] = kept.ID
	router.cloudMu.Unlock()
	if again {
		router.removeUnsent(ctx, replaced)
	}
	return nil, nil
}

// claimCloudUpload is keepSent for a cloud loop: the upload its session
// posted for this very call, taken so no other send finds it. An upload
// belongs to one loop, one session and one call, and so to one turn.
func (router *Router) claimCloudUpload(ctx context.Context, req SendRequest, path string) (*store.Attachment, *SendError, error) {
	key := cloudUploadKey{req.From.ID, req.Cloud}
	router.cloudMu.Lock()
	id, ok := router.cloudUploads[key]
	delete(router.cloudUploads, key)
	router.cloudMu.Unlock()
	if !ok || req.Cloud.ToolUseID == "" {
		return nil, &SendError{ErrAttachmentNotFound, fmt.Sprintf(
			"%s didn't reach the hub ahead of this send. If the loop's setup view says its setup script is outdated, "+
				"the operator pastes it again there; otherwise the upload may have failed on its way. Send the words alone", path)}, nil
	}
	row, err := router.store.Attachments().Get(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("read cloud upload: %w", err)
	}
	return row, nil, nil
}

// DropCloudUploads removes the uploads of loopID that no send claimed. Its
// session's Stop calls it: the call a hook posted for comes right after
// the hook or not at all, so an upload still waiting then never will be
// claimed. The hourly expiry of unsent uploads is the backstop for a turn
// that ends without a Stop.
func (router *Router) DropCloudUploads(ctx context.Context, loopID string) {
	var ids []int64
	router.cloudMu.Lock()
	for key, id := range router.cloudUploads {
		if key.loopID == loopID {
			ids = append(ids, id)
			delete(router.cloudUploads, key)
		}
	}
	router.cloudMu.Unlock()
	for _, id := range ids {
		router.removeUnsent(ctx, id)
	}
}

// removeUnsent removes an upload no message claimed, its row marked and
// its file deleted.
func (router *Router) removeUnsent(ctx context.Context, id int64) {
	row, err := router.store.Attachments().RemoveUnsent(ctx, id, time.Now().UnixMilli())
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			router.log.Warn("unsent cloud upload not removed", "upload", id, "err", err)
		}
		return
	}
	router.dropKept(row)
}
