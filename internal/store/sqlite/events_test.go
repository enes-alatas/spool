package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

func TestEventsDeleteBefore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	l := &store.Loop{ID: "l1", Name: "pruned", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Runtime: store.RuntimeBare, Pacing: store.PacingFixed,
		CreatedAt: now, UpdatedAt: now}
	if err := db.Loops().Create(ctx, l); err != nil {
		t.Fatal(err)
	}

	day := int64(24 * time.Hour / time.Millisecond)
	for _, ts := range []int64{now - 40*day, now - 35*day, now - day} {
		if _, err := db.Events().Insert(ctx, &store.Event{LoopID: "l1", TS: ts, Type: "spool", Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.Events().DeleteBefore(ctx, now-30*day)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned %d rows, want 2", n)
	}
	left, err := db.Events().ListByLoop(ctx, "l1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].TS != now-day {
		t.Fatalf("wrong survivor set: %+v", left)
	}
}
