// Package attach keeps the files that cross a chat surface (#123): once, in
// the hub's files directory, under a name the sender cannot steer outside
// it. The store records what was kept; this package owns the bytes.
package attach

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // DecodeConfig learns each format by import
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// MaxSize is the most one attachment may be: 20 MB, by operator decision
// on #123.
const MaxSize = 20 << 20

// Retention is how long a kept file stays; the row stays after it.
const Retention = 30 * 24 * time.Hour

// ErrTooLarge is a file over MaxSize. Nothing is kept.
var ErrTooLarge = errors.New("attachment over the 20 MB limit")

// Files is the hub's files directory.
type Files struct{ dir string }

// Open makes dir if it is missing. It lives inside the data directory,
// whose 0700 already covers it; 0700 here says so again for a dir passed in
// from elsewhere.
func Open(dir string) (*Files, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Files{dir: dir}, nil
}

// Keep reads r to the end and keeps it as a file named after name. It
// refuses more than MaxSize bytes with ErrTooLarge, having kept nothing.
// The returned attachment has everything but its ID, MessageID and
// CreatedAt.
func (files *Files) Keep(r io.Reader, name string) (*store.Attachment, error) {
	temp, err := os.CreateTemp(files.dir, ".incoming-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(temp.Name()) }() // a no-op once renamed

	hash := sha256.New()
	head := &headBuffer{limit: 512}
	size, err := io.Copy(io.MultiWriter(temp, hash, head), io.LimitReader(r, MaxSize+1))
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if size > MaxSize {
		return nil, ErrTooLarge
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	name = SafeName(name)
	// A random prefix, not the content hash: two messages carrying the same
	// file each own a copy, so one expiring never removes the other's.
	var prefix [8]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return nil, err
	}
	rel := hex.EncodeToString(prefix[:]) + "-" + name
	if err := os.Rename(temp.Name(), filepath.Join(files.dir, rel)); err != nil {
		return nil, err
	}

	attachment := &store.Attachment{
		Name:   name,
		MIME:   http.DetectContentType(head.bytes),
		Kind:   store.AttachmentFile,
		Size:   size,
		SHA256: sum,
		Path:   rel,
	}
	if strings.HasPrefix(attachment.MIME, "image/") {
		attachment.Kind = store.AttachmentImage
		// Only the header is read. A format the stdlib cannot decode
		// (webp) is still an image, just without its dimensions.
		if config, err := files.decodeConfig(rel); err == nil {
			attachment.Width, attachment.Height = config.Width, config.Height
		}
	}
	return attachment, nil
}

// decodeConfig reads a kept image's dimensions from its header.
func (files *Files) decodeConfig(rel string) (image.Config, error) {
	file, err := os.Open(filepath.Join(files.dir, rel))
	if err != nil {
		return image.Config{}, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	return config, err
}

// Path is where a kept file is on the host.
func (files *Files) Path(rel string) string { return filepath.Join(files.dir, rel) }

// Remove deletes a kept file. One already gone is not an error: retention
// that stopped between deleting a file and recording it runs again.
func (files *Files) Remove(rel string) error {
	if rel == "" || rel != filepath.Base(rel) {
		return fmt.Errorf("attach: %q is not a kept file", rel)
	}
	if err := os.Remove(filepath.Join(files.dir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SafeName reduces a sender's file name to one that is safe on any
// filesystem and in any shell: its base name, with every byte outside
// [A-Za-z0-9._-] replaced, no leading dot, at most 80 bytes. A name with
// nothing left is "file".
func SafeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	safe := strings.TrimLeft(out.String(), ".")
	if len(safe) > 80 {
		// keep the extension: it is what a reader opens the file by
		ext := filepath.Ext(safe)
		if len(ext) > 16 {
			ext = ""
		}
		safe = safe[:80-len(ext)] + ext
	}
	if safe == "" || safe == "_" {
		return "file"
	}
	return safe
}

// headBuffer keeps the first limit bytes written to it.
type headBuffer struct {
	limit int
	bytes []byte
}

func (head *headBuffer) Write(p []byte) (int, error) {
	if room := head.limit - len(head.bytes); room > 0 {
		head.bytes = append(head.bytes, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
