package loop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
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

// A window that has not reset only rises: a lower reading leaves its usage,
// and no reading moves its reset, earlier or later. Once it resets, the next
// reading is its value again (ADR-0047).
func TestPlanUsageOnlyRisesWithinAWindow(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	usage := NewPlanUsage(db.Settings())
	fiveHour := func(utilization float64, resetsAt, at int64) *store.PlanUsageWindow {
		t.Helper()
		if err := usage.Observe(ctx, store.PlanUsageRecord{
			FiveHour: &store.PlanUsageWindow{Utilization: utilization, ResetsAt: resetsAt}, At: at, Source: "stream",
		}); err != nil {
			t.Fatal(err)
		}
		record, err := LastPlanUsage(ctx, db.Settings())
		if err != nil {
			t.Fatal(err)
		}
		return record.FiveHour
	}

	fiveHour(0.6, 5000, 1000)
	for _, row := range []struct {
		name         string
		utilization  float64
		resetsAt, at int64
		wantUse      float64
		wantResetsAt int64
	}{
		{"a lower reading", 0.1, 5000, 1100, 0.6, 5000},
		{"an earlier reset", 0.6, 1200, 1150, 0.6, 5000},
		{"a later reset", 0.7, 90000, 1200, 0.7, 5000},
		{"after the reset", 0.1, 23000, 5000, 0.1, 23000},
	} {
		if got := fiveHour(row.utilization, row.resetsAt, row.at); got == nil ||
			got.Utilization != row.wantUse || got.ResetsAt != row.wantResetsAt {
			t.Errorf("%s: five_hour = %+v; want %v resetting at %d", row.name, got, row.wantUse, row.wantResetsAt)
		}
	}
}

// A reading's window is refused when it can't be one of its span: a reset
// already past or further off than the span, or a negative usage.
func TestPlanUsageFromRefusesAWindowOutsideItsSpan(t *testing.T) {
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC).UnixMilli()
	in := func(after time.Duration) int64 { return (at + after.Milliseconds()) / 1000 }
	for _, row := range []struct {
		name     string
		fiveHour claude.UnifiedWindow
		kept     bool
	}{
		{"five hours out", claude.UnifiedWindow{Utilization: 0.5, ResetsAt: in(5 * time.Hour)}, true},
		{"inside the slack", claude.UnifiedWindow{Utilization: 0.5, ResetsAt: in(5*time.Hour + 10*time.Minute)}, true},
		{"a day out", claude.UnifiedWindow{Utilization: 0.99, ResetsAt: in(24 * time.Hour)}, false},
		{"far past any day", claude.UnifiedWindow{Utilization: 0.99, ResetsAt: 1 << 62}, false},
		{"already past", claude.UnifiedWindow{Utilization: 0.1, ResetsAt: in(-time.Minute)}, false},
		{"a negative usage", claude.UnifiedWindow{Utilization: -1, ResetsAt: in(time.Hour)}, false},
	} {
		seven := claude.UnifiedWindow{Utilization: 0.2, ResetsAt: in(6 * 24 * time.Hour)}
		fiveHour := row.fiveHour
		record, ok := planUsageFrom(&claude.RateLimitInfo{UnifiedWindows: &claude.UnifiedWindows{FiveHour: &fiveHour, SevenDay: &seven}}, at)
		if !ok || record.SevenDay == nil || (record.FiveHour != nil) != row.kept {
			t.Errorf("%s: record = %+v, %v; want five_hour kept %v beside seven_day", row.name, record, ok, row.kept)
		}
	}
	far := claude.UnifiedWindow{Utilization: 0.99, ResetsAt: in(30 * 24 * time.Hour)}
	if _, ok := planUsageFrom(&claude.RateLimitInfo{UnifiedWindows: &claude.UnifiedWindows{SevenDay: &far}}, at); ok {
		t.Error("a seven-day window resetting in thirty days was kept")
	}
}
