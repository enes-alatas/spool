package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestAttachmentsOutliveTheirFiles pins the retention contract (#123):
// expiry marks the rows it returns as removed and leaves them readable, and
// an attachment already expired is not returned a second time.
func TestAttachmentsOutliveTheirFiles(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	table := db.Attachments()

	old := &store.Attachment{MessageID: 7, Name: "old.png", MIME: "image/png", Kind: store.AttachmentImage,
		Size: 10, Width: 4, Height: 2, Path: "aa-old.png", CreatedAt: 1_000}
	fresh := &store.Attachment{MessageID: 7, Name: "fresh.txt", Kind: store.AttachmentFile, Size: 3, Path: "bb-fresh.txt", CreatedAt: 5_000}
	for _, attachment := range []*store.Attachment{old, fresh} {
		if err := table.Insert(ctx, attachment); err != nil {
			t.Fatal(err)
		}
	}
	if old.ID == 0 || fresh.ID <= old.ID {
		t.Fatalf("ids %d, %d: want increasing and set", old.ID, fresh.ID)
	}

	expired, err := table.Expire(ctx, 2_000, 9_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != old.ID || expired[0].Path != "aa-old.png" || expired[0].RemovedAt != 9_000 {
		t.Fatalf("expired %+v, want the old one, marked", expired)
	}
	if again, _ := table.Expire(ctx, 2_000, 9_500); len(again) != 0 {
		t.Errorf("expired %d a second time", len(again))
	}

	rows, err := table.ByMessage(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "old.png" || rows[0].RemovedAt != 9_000 || rows[0].Width != 4 || rows[1].RemovedAt != 0 {
		t.Errorf("rows %+v, want both, in order, the old one removed", rows)
	}
	if _, err := table.Get(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(999) err = %v, want ErrNotFound", err)
	}
}

// TestAnUploadWaitsForOneMessage pins the control room's upload (#460): an
// upload waits unclaimed, one message claims it, and a second claim, or a
// claim after expiry, is refused. Expiry of unsent uploads leaves sent ones.
func TestAnUploadWaitsForOneMessage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	table := db.Attachments()

	sent := &store.Attachment{Name: "a.png", Kind: store.AttachmentImage, Size: 1, Path: "aa-a.png", CreatedAt: 1_000}
	unsent := &store.Attachment{Name: "b.txt", Kind: store.AttachmentFile, Size: 1, Path: "bb-b.txt", CreatedAt: 1_000}
	other := &store.Attachment{MessageID: 9, Name: "c.txt", Kind: store.AttachmentFile, Size: 1, Path: "cc-c.txt", CreatedAt: 1_000}
	for _, attachment := range []*store.Attachment{sent, unsent, other} {
		if err := table.Insert(ctx, attachment); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.Claim(ctx, sent.ID, 7); err != nil {
		t.Fatalf("claim a waiting upload: %v", err)
	}
	for _, id := range []int64{sent.ID, other.ID, 999} {
		if err := table.Claim(ctx, id, 8); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("claim %d: %v, want ErrNotFound", id, err)
		}
	}

	expired, err := table.ExpireUnsent(ctx, 2_000, 3_000)
	if err != nil || len(expired) != 1 || expired[0].ID != unsent.ID {
		t.Fatalf("expired %+v, %v; want the unsent upload alone", expired, err)
	}
	if err := table.Claim(ctx, unsent.ID, 8); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("claim of an expired upload: %v, want ErrNotFound", err)
	}

	byMessage, err := table.ByMessages(ctx, []int64{7, 9, 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(byMessage) != 2 || byMessage[7][0].ID != sent.ID || byMessage[9][0].ID != other.ID {
		t.Errorf("by message %+v", byMessage)
	}
}
