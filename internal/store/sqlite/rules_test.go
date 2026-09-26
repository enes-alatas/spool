package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestFleetRulesRoundTrip pins the fleet rule store: Create inserts, List
// returns every rule in creation order regardless of enabled state, Update
// rewrites text and the flag in place, and Delete removes one.
func TestFleetRulesRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	rules := db.FleetRules()

	if got, err := rules.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh store: got %d rules %v, want none", len(got), err)
	}

	first := &store.FleetRule{ID: "rule_1", Title: "sign your work", Body: "End every artifact with your name.",
		Enabled: true, CreatedAt: 10, UpdatedAt: 10}
	second := &store.FleetRule{ID: "rule_2", Title: "one PR at a time", Body: "Never open a second PR.",
		Enabled: false, CreatedAt: 20, UpdatedAt: 20}
	for _, rule := range []*store.FleetRule{second, first} { // inserted out of order on purpose
		if err := rules.Create(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}

	got, err := rules.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "rule_1" || got[1].ID != "rule_2" {
		t.Fatalf("List = %+v, want [rule_1 rule_2] in creation order", got)
	}
	if !got[0].Enabled || got[1].Enabled {
		t.Fatalf("enabled flags did not round-trip: %+v", got)
	}

	second.Title, second.Enabled, second.UpdatedAt = "one open PR at a time", true, 30
	if err := rules.Update(ctx, second); err != nil {
		t.Fatal(err)
	}
	updated, err := rules.Get(ctx, "rule_2")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "one open PR at a time" || !updated.Enabled || updated.UpdatedAt != 30 {
		t.Fatalf("Update did not land: %+v", updated)
	}

	if err := rules.Update(ctx, &store.FleetRule{ID: "rule_missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Update of an unknown id: err = %v, want ErrNotFound", err)
	}
	if _, err := rules.Get(ctx, "rule_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get of an unknown id: err = %v, want ErrNotFound", err)
	}

	if err := rules.Delete(ctx, "rule_1"); err != nil {
		t.Fatal(err)
	}
	got, err = rules.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "rule_2" {
		t.Fatalf("after delete: got %+v, want only rule_2", got)
	}
}
