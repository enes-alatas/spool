package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// TestLoopWorkstationColumns pins the workstation config contract (ADR-0018):
// runtime/image/limits round-trip through Create, and Update cannot touch
// them — immutability is enforced by omission from the UPDATE statement.
func TestLoopWorkstationColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l1", Name: "walled", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeDocker, Image: "spool-workstation", MemMB: 4096, CPUs: 2,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}

	got, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != store.RuntimeDocker || got.Image != "spool-workstation" ||
		got.MemMB != 4096 || got.CPUs != 2 {
		t.Fatalf("workstation config did not round-trip: %+v", got)
	}

	got.Runtime = store.RuntimeBare
	got.Image = "other"
	got.MemMB = 1
	got.CPUs = 1
	got.Mission = "updated"
	if err := db.Loops().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, err := db.Loops().Get(ctx, "l1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Mission != "updated" {
		t.Fatalf("mission not updated: %+v", after)
	}
	if after.Runtime != store.RuntimeDocker || after.Image != "spool-workstation" ||
		after.MemMB != 4096 || after.CPUs != 2 {
		t.Fatalf("workstation config must be immutable through Update: %+v", after)
	}
}

// TestLoopRuntimeChecked pins the CHECK constraint: only bare|docker are
// storable runtimes, so a caller bug cannot persist an unknown kind.
func TestLoopRuntimeChecked(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UnixMilli()
	loop := &store.Loop{
		ID: "l2", Name: "bogus", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: "microvm", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Loops().Create(context.Background(), loop); err == nil {
		t.Fatal("creating a loop with an unknown runtime must fail")
	}
}

// TestLoopHubMCPToken pins the send-token contract (ADR-0026): Create mints a
// token when the caller leaves it empty, the token resolves its loop, and an
// unknown or empty bearer is ErrNotFound.
func TestLoopHubMCPToken(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UnixMilli()
	for _, name := range []string{"terra", "iris"} {
		l := &store.Loop{
			ID: "l_" + name, Name: name, Mission: "m", Status: store.StatusActive,
			WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
			Runtime: store.RuntimeBare, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Loops().Create(ctx, l); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if l.HubMCPToken == "" {
			t.Fatalf("create %s left HubMCPToken empty", name)
		}
	}

	terra, err := db.Loops().GetByName(ctx, "terra")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Loops().GetByHubMCPToken(ctx, terra.HubMCPToken)
	if err != nil || got.ID != terra.ID {
		t.Fatalf("GetByHubMCPToken = %v, %v; want terra", got, err)
	}
	if _, err := db.Loops().GetByHubMCPToken(ctx, "nope"); err != store.ErrNotFound {
		t.Fatalf("unknown token: err = %v, want ErrNotFound", err)
	}
	if _, err := db.Loops().GetByHubMCPToken(ctx, ""); err != store.ErrNotFound {
		t.Fatalf("empty token: err = %v, want ErrNotFound", err)
	}
}
