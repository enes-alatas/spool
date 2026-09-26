package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
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
	loopRecord := &store.Loop{ID: "l1", Name: "pruned", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Runtime: store.RuntimeBare, Pacing: store.PacingFixed,
		CreatedAt: now, UpdatedAt: now}
	if err := db.Loops().Create(ctx, loopRecord); err != nil {
		t.Fatal(err)
	}

	day := int64(24 * time.Hour / time.Millisecond)
	for _, ts := range []int64{now - 40*day, now - 35*day, now - day} {
		if _, err := db.Events().Insert(ctx, &store.Event{LoopID: "l1", TS: ts, Type: "spool", Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := db.Events().DeleteBefore(ctx, now-30*day)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("pruned %d rows, want 2", deleted)
	}
	left, err := db.Events().ListByLoop(ctx, "l1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].TS != now-day {
		t.Fatalf("wrong survivor set: %+v", left)
	}
}

// TestEventsNewestWindow pins the newest-window read (#119): before_id 0 is
// the newest end of the timeline, a cursor pages older from there, both come
// back oldest first, and a loop with fewer events than the limit is not a
// special case.
func TestEventsNewestWindow(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	for _, id := range []string{"l1", "l2"} {
		loopRecord := &store.Loop{ID: id, Name: "loop-" + id, Mission: "m", Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Runtime: store.RuntimeBare, Pacing: store.PacingFixed,
			CreatedAt: now, UpdatedAt: now}
		if err := db.Loops().Create(ctx, loopRecord); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	for i := range 10 {
		id, err := db.Events().Insert(ctx, &store.Event{LoopID: "l1", TS: now + int64(i), Type: "spool",
			Subtype: "e" + strconv.Itoa(i), Payload: "{}"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// a second loop's events share the table and must never share a window
	if _, err := db.Events().Insert(ctx, &store.Event{LoopID: "l2", TS: now, Type: "spool", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}

	newest, err := db.Events().ListByLoopBefore(ctx, "l1", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(newest) != 3 || newest[0].ID != ids[7] || newest[2].ID != ids[9] {
		t.Fatalf("newest window = %s, want the last three oldest-first", dumpIDs(newest))
	}

	older, err := db.Events().ListByLoopBefore(ctx, "l1", newest[0].ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 3 || older[0].ID != ids[4] || older[2].ID != ids[6] {
		t.Fatalf("page before the window = %s, want the three preceding it", dumpIDs(older))
	}

	all, err := db.Events().ListByLoopBefore(ctx, "l1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 {
		t.Fatalf("a limit past the loop's history returned %d events, want all 10", len(all))
	}
}

func dumpIDs(events []*store.Event) string {
	var ids strings.Builder
	for i, event := range events {
		if i > 0 {
			ids.WriteString(",")
		}
		ids.WriteString(strconv.FormatInt(event.ID, 10))
	}
	return "[" + ids.String() + "]"
}
