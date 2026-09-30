package route

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// InboundAttachment is a file a surface saw on an inbound message (#123).
// The bytes are fetched lazily: a message several surfaces hear is stored
// once, by whichever ingests it first, and only that one downloads it.
type InboundAttachment struct {
	Name string
	// Kind is the surface's word for it when it has one (a Telegram photo
	// is an image before a byte is read); "" leaves it to the content.
	Kind string
	// Size is what the surface declared, 0 when it did not say. A declared
	// size over the limit is refused without a download.
	Size  int64
	Fetch func(ctx context.Context) (io.ReadCloser, error)
}

// fetchTimeout bounds one attachment's download. 20 MB over a slow link is
// well inside it; a surface that stalls longer is not handing it over.
const fetchTimeout = 2 * time.Minute

// SetFiles gives the router the hub's files directory. Without one, an
// attachment is recorded as not kept: the message still says it came.
//
// redactName cleans a sender's file name before anything is derived from
// it. The name becomes the row's name, the kept file's path and the line a
// loop reads, and SafeName rewrites it on the way, so a secret in it has to
// be caught as sent: the store's redaction of the row comes too late to
// match it.
func (router *Router) SetFiles(files *attach.Files, redactName func(string) string) {
	router.files = files
	router.redactName = redactName
}

// keepAttachments fetches and keeps a stored message's attachments and
// records each, kept or not. A file that cannot be kept is recorded with
// the reason, so the loop is told something was sent rather than nothing.
func (router *Router) keepAttachments(ctx context.Context, messageID int64, inbound []InboundAttachment) []*store.Attachment {
	var rows []*store.Attachment
	for _, item := range inbound {
		row := router.keep(ctx, item)
		row.MessageID = messageID
		row.CreatedAt = time.Now().UnixMilli()
		if err := router.store.Attachments().Insert(ctx, row); err != nil {
			router.log.Warn("attachment not recorded", "message", messageID, "err", err)
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func (router *Router) keep(ctx context.Context, item InboundAttachment) *store.Attachment {
	if router.redactName != nil {
		item.Name = router.redactName(item.Name)
	}
	notKept := func(reason string, size int64) *store.Attachment {
		return &store.Attachment{Name: attach.SafeName(item.Name), Kind: guessKind(item), Size: size, NotKept: reason}
	}
	if item.Size > attach.MaxSize {
		return notKept(store.NotKeptTooLarge, item.Size)
	}
	if router.files == nil || item.Fetch == nil {
		return notKept(store.NotKeptFailed, item.Size)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	body, err := item.Fetch(ctx)
	if err != nil {
		router.log.Warn("attachment not fetched", "name", item.Name, "err", err)
		return notKept(store.NotKeptFailed, item.Size)
	}
	defer body.Close()
	kept, err := router.files.Keep(body, item.Name)
	switch {
	case errors.Is(err, attach.ErrTooLarge):
		return notKept(store.NotKeptTooLarge, max(item.Size, attach.MaxSize+1))
	case err != nil:
		router.log.Warn("attachment not kept", "name", item.Name, "err", err)
		return notKept(store.NotKeptFailed, item.Size)
	}
	// What the bytes say wins over what the surface called them.
	return kept
}

// guessKind names a file that never arrived, from what the surface said
// or failing that its extension; nothing better is left to go on.
func guessKind(item InboundAttachment) string {
	if item.Kind != "" {
		return item.Kind
	}
	switch strings.ToLower(filepath.Ext(item.Name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return store.AttachmentImage
	}
	return store.AttachmentFile
}

// present is what target is shown of a message's attachments, and the
// copies its workstation needs before it reads them.
func (router *Router) present(target *store.Loop, rows []*store.Attachment) ([]loop.Attachment, []loop.FileCopy) {
	hostPath := func(rel string) string { return rel }
	if router.files != nil {
		hostPath = router.files.Path
	}
	return loop.PresentAttachments(target, rows, hostPath)
}

// ExpireAttachments removes every kept file older than attach.Retention,
// keeping the rows (#123). Run hourly by the wiring, next to event pruning.
func (router *Router) ExpireAttachments(ctx context.Context) (int, error) {
	if router.files == nil {
		return 0, nil
	}
	now := time.Now()
	expired, err := router.store.Attachments().Expire(ctx, now.Add(-attach.Retention).UnixMilli(), now.UnixMilli())
	if err != nil {
		return 0, err
	}
	for _, row := range expired {
		if err := router.files.Remove(row.Path); err != nil {
			router.log.Warn("expired attachment not removed", "id", row.ID, "err", err)
		}
	}
	return len(expired), nil
}

// Workstations reads the file a loop sends out of its workstation
// (loop.Manager).
type Workstations interface {
	GetFile(ctx context.Context, loopRecord *store.Loop, path string, limit int64) ([]byte, error)
}

// SetWorkstations lets the router read a file a loop sends. Without it, a
// send with an attachment is refused.
func (router *Router) SetWorkstations(workstations Workstations) { router.workstations = workstations }

// keepSent reads the file a send attaches out of the sender's workstation
// and keeps it, before the message is stored (#123). Every reason it is
// refused is the loop's to correct, so each is a SendError naming what to
// change. The kept attachment has no MessageID yet, and it is the caller's
// to record or, if the send goes no further, to remove.
func (router *Router) keepSent(ctx context.Context, req SendRequest) (*store.Attachment, *SendError, error) {
	path := strings.TrimSpace(req.Attach)
	if path == "" {
		return nil, nil, nil
	}
	if router.files == nil || router.workstations == nil {
		return nil, &SendError{ErrAttachmentUnavailable, "this hub keeps no files, so a message cannot carry one; send the words alone"}, nil
	}
	body, err := router.workstations.GetFile(ctx, req.From, path, attach.MaxSize)
	switch {
	case errors.Is(err, runtime.ErrNotOwned):
		return nil, &SendError{ErrAttachmentNotOwned,
			fmt.Sprintf("%s is not a file you own; attach a file inside your workspace, %s", path, req.From.WorkspacePath)}, nil
	case errors.Is(err, runtime.ErrNoSuchFile):
		return nil, &SendError{ErrAttachmentNotFound, fmt.Sprintf("there is no file at %s", path)}, nil
	case errors.Is(err, runtime.ErrNotAFile):
		return nil, &SendError{ErrAttachmentNotFound, fmt.Sprintf("%s is not a regular file; attach one file", path)}, nil
	case errors.Is(err, runtime.ErrFileTooLarge):
		return nil, &SendError{ErrAttachmentTooLarge, fmt.Sprintf("%s is over the %d MB limit", path, attach.MaxSize>>20)}, nil
	case err != nil:
		return nil, nil, fmt.Errorf("read attachment: %w", err)
	}
	name := filepath.Base(path)
	if router.redactName != nil {
		// A file's bytes never pass through redaction on their way out,
		// so a file holding a secret the hub knows is not sent at all.
		if router.redactName(string(body)) != string(body) {
			return nil, &SendError{ErrSecretInAttachment,
				fmt.Sprintf("%s holds a secret this hub knows, and a file is never sent with one; attach a copy without it", path)}, nil
		}
		name = router.redactName(name)
	}
	kept, err := router.files.Keep(bytes.NewReader(body), name)
	if err != nil {
		return nil, nil, fmt.Errorf("keep attachment: %w", err)
	}
	return kept, nil, nil
}

// SentAttachment is the file a loop's message carries, for a surface to
// send with it: the row and the hub's copy of it. It answers nil for a
// message with none. A file that is no longer kept is an error, since the
// message cannot be sent as it was written.
func (router *Router) SentAttachment(ctx context.Context, messageID int64) (*store.Attachment, string, error) {
	rows, err := router.store.Attachments().ByMessage(ctx, messageID)
	if err != nil || len(rows) == 0 {
		return nil, "", err
	}
	row := rows[0]
	if row.Path == "" || row.RemovedAt != 0 || router.files == nil {
		return nil, "", ErrAttachmentGone
	}
	return row, router.files.Path(row.Path), nil
}

// ErrAttachmentGone fails the send of a message whose file is no longer
// kept: retention removed it before a retry.
var ErrAttachmentGone = errors.New("the attached file is no longer kept")

// dropKept removes a file kept for a send that went no further.
func (router *Router) dropKept(kept *store.Attachment) {
	if kept == nil {
		return
	}
	if err := router.files.Remove(kept.Path); err != nil {
		router.log.Warn("unsent attachment not removed", "path", kept.Path, "err", err)
	}
}
