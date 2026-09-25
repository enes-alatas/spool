package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A resolution is one row per model, replaced in place; a custom entry is
// unique by model, and deleting it takes its resolution along (#332).
func TestModelStore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	models := db.Models()

	probe := &store.ModelResolution{Model: "opus", Resolved: "claude-opus-5-5",
		Source: store.ResolutionProbe, CLIVersion: "2.1.281", ResolvedAt: 1}
	if err := models.SetResolution(ctx, probe); err != nil {
		t.Fatal(err)
	}
	turn := &store.ModelResolution{Model: "opus", Resolved: "claude-opus-5-6",
		Source: store.ResolutionTurn, ResolvedAt: 2}
	if err := models.SetResolution(ctx, turn); err != nil {
		t.Fatal(err)
	}
	got, err := models.Resolutions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || *got[0] != *turn {
		t.Fatalf("resolutions = %+v, want the turn's replacing the probe's", got)
	}

	pinned := &store.CustomModel{ID: "cm_1", Model: "claude-opus-4-1", Label: "pinned", CreatedAt: 3}
	if err := models.AddCustom(ctx, pinned); err != nil {
		t.Fatal(err)
	}
	if err := models.AddCustom(ctx, &store.CustomModel{ID: "cm_2", Model: "claude-opus-4-1", CreatedAt: 4}); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("second entry for the same model: err = %v, want ErrDuplicate", err)
	}
	relabeled, err := models.SetCustomLabel(ctx, "cm_1", "Opus 4.1")
	if err != nil {
		t.Fatal(err)
	}
	if relabeled.Label != "Opus 4.1" || relabeled.Model != pinned.Model {
		t.Fatalf("relabeled = %+v", relabeled)
	}
	if _, err := models.SetCustomLabel(ctx, "cm_none", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("relabel of a missing entry: err = %v, want ErrNotFound", err)
	}

	if err := models.SetResolution(ctx, &store.ModelResolution{Model: "claude-opus-4-1",
		Resolved: "claude-opus-4-1", Source: store.ResolutionProbe, ResolvedAt: 5}); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteCustom(ctx, "cm_1"); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteCustom(ctx, "cm_1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
	list, err := models.ListCustom(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, err = models.Resolutions(ctx); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 || len(got) != 1 || got[0].Model != "opus" {
		t.Fatalf("after delete: custom %+v, resolutions %+v; want none, and opus's alone", list, got)
	}
}
