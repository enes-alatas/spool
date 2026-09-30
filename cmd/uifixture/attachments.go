package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/store"
)

// A file that came with a fixture message, in one of the states the room
// draws differently (#460): kept (an image the page thumbnails, a file it
// links), removed by retention, or never kept at all.
type fixtureFile struct {
	name string
	// body is the bytes, kept through attach.Files the way an arriving file
	// is, so the hub serves them and the store row carries what the bytes
	// say (MIME, image dimensions) rather than what the fixture claims.
	body []byte
	// removed drops the kept bytes again, as retention does 30 days on:
	// the row stays and says when.
	removed bool
	// notKept is why a file never arrived; its size is what the surface
	// declared, and there are no bytes.
	notKept string
	size    int64
}

// retention is how long the hub keeps a file; a removed fixture file is
// dated by it.
const retention = 30 * 24 * time.Hour

func keepFixtureFiles(ctx context.Context, db store.Store, files *attach.Files, messageID int64, at time.Duration, list []fixtureFile) error {
	for _, file := range list {
		var row *store.Attachment
		if file.notKept != "" {
			row = &store.Attachment{Name: file.name, Kind: store.AttachmentFile, Size: file.size, NotKept: file.notKept}
		} else {
			kept, err := files.Keep(bytes.NewReader(file.body), file.name)
			if err != nil {
				return fmt.Errorf("keep %s: %w", file.name, err)
			}
			row = kept
			if file.removed {
				if err := files.Remove(row.Path); err != nil {
					return fmt.Errorf("remove %s: %w", file.name, err)
				}
				row.RemovedAt = ms(at + retention)
			}
		}
		row.MessageID = messageID
		row.CreatedAt = ms(at)
		if err := db.Attachments().Insert(ctx, row); err != nil {
			return fmt.Errorf("attachment %s: %w", file.name, err)
		}
	}
	return nil
}

// fixtureScreenshot draws a web page whose primary button runs off its
// right edge: an invented page, so the thumbnail the room shows is of
// nobody's product. PNG, so the hub reads its dimensions from the header.
func fixtureScreenshot() []byte {
	page := image.NewRGBA(image.Rect(0, 0, 1200, 675))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		draw.Draw(page, image.Rect(x0, y0, x1, y1), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	fill(0, 0, 1200, 675, color.RGBA{245, 246, 248, 255})
	fill(0, 0, 1200, 60, color.RGBA{36, 41, 47, 255})
	for _, x := range []int{40, 180, 320} {
		fill(x, 22, x+100, 38, color.RGBA{80, 88, 98, 255})
	}
	for _, y := range []int{110, 320} {
		fill(80, y, 1120, y+170, color.RGBA{255, 255, 255, 255})
		for i, width := range []int{520, 660, 440} {
			fill(116, y+36+i*36, 116+width, y+52+i*36, color.RGBA{214, 218, 224, 255})
		}
	}
	fill(980, 560, 1200, 612, color.RGBA{46, 125, 246, 255})
	var out bytes.Buffer
	if err := png.Encode(&out, page); err != nil {
		panic(err) // an in-memory encode of a well-formed image cannot fail
	}
	return out.Bytes()
}

// fixtureLog is the text a nightly run might have written, so the file
// line has a plausible size and the download serves something readable.
func fixtureLog() []byte {
	var out bytes.Buffer
	for i := range 40 {
		fmt.Fprintf(&out, "02:%02d:%02d fixture docs/install: step %d ok\n", 10+i/30, (i*7)%60, i+1)
	}
	out.WriteString("02:11:52 fixture docs/install: missing fixture testdata/install.golden\nFAIL\n")
	return out.Bytes()
}
