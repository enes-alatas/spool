package sched

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// tickCounter records which loops were ticked.
type tickCounter struct{ ticked chan string }

func (counter tickCounter) Tick(loopID string) bool {
	counter.ticked <- loopID
	return true
}

func (tickCounter) CheckWakes() {}

// Only the ticks that came due while the hub was down are pushed out: a
// tick asked for once the scheduler runs, as a new loop's first is, fires
// at once (#595). Run used to rescan on start, beside the API, and jittered
// such a tick up to a minute out.
func TestATickAskedForOnceTheSchedulerRunsFiresAtOnce(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for _, id := range []string{"overdue", "fresh"} {
		loopRecord := store.Loop{
			ID: id, Name: id, Mission: "m", Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
			Runtime: store.RuntimeBare, TickIntervalSec: 3600, CreatedAt: 1, UpdatedAt: 1,
		}
		if err := db.Loops().Create(ctx, &loopRecord); err != nil {
			t.Fatal(err)
		}
	}
	downSince := time.Now().Add(-time.Hour).UnixMilli()
	if err := db.Schedule().Set(ctx, "overdue", downSince); err != nil {
		t.Fatal(err)
	}

	counter := tickCounter{ticked: make(chan string, 8)}
	scheduler := New(db, bus.New(), counter, nil)
	scheduler.Recover(ctx)
	entry, err := db.Schedule().Get(ctx, "overdue")
	if err != nil || entry.NextTickAt <= downSince {
		t.Fatalf("the tick due while the hub was down = %+v, %v; want it moved past now", entry, err)
	}
	// stand the overdue tick out of the way, so only the fresh one can fire
	if err := db.Schedule().Set(ctx, "overdue", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// asked for before Run has looked at the schedule: the race itself
	scheduler.ScheduleNow("fresh")
	go scheduler.Run(ctx)
	select {
	case loopID := <-counter.ticked:
		if loopID != "fresh" {
			t.Errorf("ticked %s, want fresh", loopID)
		}
	case <-time.After(5 * time.Second):
		t.Error("a tick asked for once the scheduler runs did not fire within 5s")
	}
}
