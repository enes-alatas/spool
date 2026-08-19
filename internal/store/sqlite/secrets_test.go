package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// TestLoopSecretsRoundTrip pins the per-loop secret store: Set inserts,
// a second Set on the same name upserts the value, List returns names sorted
// with values intact for the injector, and Delete removes one.
func TestLoopSecretsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	loop := &store.Loop{
		ID: "l1", Name: "walled", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	secrets := db.LoopSecrets()

	if got, err := secrets.List(ctx, "l1"); err != nil || len(got) != 0 {
		t.Fatalf("fresh loop: got %d secrets %v, want none", len(got), err)
	}

	if err := secrets.Set(ctx, "l1", "GH_TOKEN", "ghp_first", now); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set(ctx, "l1", "API_KEY", "hunter2", now); err != nil {
		t.Fatal(err)
	}
	// A second Set on the same name upserts in place.
	if err := secrets.Set(ctx, "l1", "GH_TOKEN", "ghp_second", now+1); err != nil {
		t.Fatal(err)
	}

	got, err := secrets.List(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "API_KEY" || got[1].Name != "GH_TOKEN" {
		t.Fatalf("List = %+v, want [API_KEY GH_TOKEN] sorted", got)
	}
	if got[1].Value != "ghp_second" || got[1].UpdatedAt != now+1 {
		t.Fatalf("upsert did not replace value/timestamp: %+v", got[1])
	}

	if err := secrets.Delete(ctx, "l1", "API_KEY"); err != nil {
		t.Fatal(err)
	}
	got, err = secrets.List(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "GH_TOKEN" {
		t.Fatalf("after delete: got %+v, want only GH_TOKEN", got)
	}
}

// TestLoopSecretsCascade proves deleting a loop takes its secrets with it
// (ON DELETE CASCADE), so no orphaned credentials linger in the DB.
func TestLoopSecretsCascade(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	loop := &store.Loop{
		ID: "l1", Name: "walled", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed, Runtime: store.RuntimeBare,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	if err := db.LoopSecrets().Set(ctx, "l1", "GH_TOKEN", "ghp_x", now); err != nil {
		t.Fatal(err)
	}
	if err := db.Loops().Delete(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if got, err := db.LoopSecrets().List(ctx, "l1"); err != nil || len(got) != 0 {
		t.Fatalf("after loop delete: got %d secrets %v, want none (cascade)", len(got), err)
	}
}
