package loop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// TestPlanUsageKeepsTheNewestObservation: an older observation never
// overwrites a newer one, and a window an observation leaves out keeps its
// last value until that window resets.
func TestPlanUsageKeepsTheNewestObservation(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	usage := NewPlanUsage(db.Settings())

	if record, err := LastPlanUsage(ctx, db.Settings()); err != nil || record.At != 0 {
		t.Fatalf("before any observation = %+v, %v; want the zero record", record, err)
	}
	observe := func(record store.PlanUsageRecord) {
		t.Helper()
		if err := usage.Observe(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	observe(store.PlanUsageRecord{
		FiveHour: &store.PlanUsageWindow{Utilization: 0.2, ResetsAt: 5000},
		SevenDay: &store.PlanUsageWindow{Utilization: 0.4, ResetsAt: 90000},
		At:       2000, Source: "stream",
	})
	observe(store.PlanUsageRecord{FiveHour: &store.PlanUsageWindow{Utilization: 0.9, ResetsAt: 5000}, At: 1000, Source: "stream"})
	record, err := LastPlanUsage(ctx, db.Settings())
	if err != nil {
		t.Fatal(err)
	}
	if record.At != 2000 || record.FiveHour.Utilization != 0.2 {
		t.Fatalf("an older observation overwrote a newer one: %+v", record)
	}

	observe(store.PlanUsageRecord{FiveHour: &store.PlanUsageWindow{Utilization: 0.3, ResetsAt: 5000}, At: 3000, Source: "stream"})
	if record, _ = LastPlanUsage(ctx, db.Settings()); record.SevenDay == nil || record.SevenDay.Utilization != 0.4 {
		t.Fatalf("a window the observation left out was dropped: %+v", record)
	}

	observe(store.PlanUsageRecord{SevenDay: &store.PlanUsageWindow{Utilization: 0.5, ResetsAt: 90000}, At: 6000, Source: "stream"})
	if record, _ = LastPlanUsage(ctx, db.Settings()); record.FiveHour != nil {
		t.Fatalf("a window past its reset was carried forward: %+v", record.FiveHour)
	}
}
