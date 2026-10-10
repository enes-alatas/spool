package sqlite

import (
	"context"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestLoopChangesNameWhoActed: a create names its actor as who made the
// loop and who last changed it, any later change as who last changed it,
// and a wake neither; an edit of the loop's own fields leaves both alone.
// The record reads newest first, narrowed by loop id or by the name a
// deleted loop had, and outlives the loop.
func TestLoopChangesNameWhoActed(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.Loops().Create(ctx, &store.Loop{
		ID: "l1", Name: "aster", Status: store.StatusActive, WorkspaceMode: store.WorkspaceNone,
		Runtime: store.RuntimeBare, Pacing: store.PacingFixed, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	changes := db.LoopChanges()
	record := func(at int64, by, action string) {
		t.Helper()
		if err := changes.Record(ctx, &store.LoopChange{At: at, By: by, Action: action, LoopID: "l1", LoopName: "aster"}); err != nil {
			t.Fatal(err)
		}
	}
	names := func() (string, string) {
		t.Helper()
		loopRecord, err := db.Loops().Get(ctx, "l1")
		if err != nil {
			t.Fatal(err)
		}
		return loopRecord.CreatedBy, loopRecord.UpdatedBy
	}

	record(10, "olive", store.LoopChangeCreate)
	if created, updated := names(); created != "olive" || updated != "olive" {
		t.Fatalf("after the create: created_by %q, updated_by %q; want olive twice", created, updated)
	}
	record(20, "mia", store.LoopChangeWake)
	record(30, "operator", store.LoopChangePause)
	mission := "a new mission"
	if _, err := db.Loops().Edit(ctx, "l1", store.LoopEdit{Mission: &mission}); err != nil {
		t.Fatal(err)
	}
	if created, updated := names(); created != "olive" || updated != "operator" {
		t.Fatalf("after a wake, a pause and an edit: created_by %q, updated_by %q; want olive, operator", created, updated)
	}

	all, err := changes.List(ctx, store.LoopChangeFilter{LoopID: "l1"})
	if err != nil || len(all) != 3 || all[0].Action != store.LoopChangePause || all[0].By != "operator" ||
		all[2].Action != store.LoopChangeCreate || all[2].LoopName != "aster" {
		t.Fatalf("the record = %+v, %v; want pause, wake, create", all, err)
	}
	if newest, err := changes.List(ctx, store.LoopChangeFilter{Limit: 1}); err != nil || len(newest) != 1 || newest[0].At != 30 {
		t.Fatalf("limit 1 = %+v, %v; want the pause", newest, err)
	}
	if err := db.Loops().Delete(ctx, "l1", 40); err != nil {
		t.Fatal(err)
	}
	record(40, "olive", store.LoopChangeDelete)
	if gone, err := changes.List(ctx, store.LoopChangeFilter{LoopName: "aster"}); err != nil || len(gone) != 4 || gone[0].Action != store.LoopChangeDelete {
		t.Fatalf("a deleted loop's record = %+v, %v; want four changes, the delete first", gone, err)
	}
}
