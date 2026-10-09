package loop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

func TestCapOf(t *testing.T) {
	usage := store.PlanUsageRecord{
		FiveHour: &store.PlanUsageWindow{Utilization: 0.92, ResetsAt: 5000},
		SevenDay: &store.PlanUsageWindow{Utilization: 0.95, ResetsAt: 90000},
		At:       2000,
	}
	tests := []struct {
		name               string
		usage              store.PlanUsageRecord
		fiveHour, sevenDay int
		resumedUntil, now  int64
		wantWindows        []string
		wantUntil          int64
		wantHolds          bool
	}{
		{
			name:     "unknown usage never caps",
			usage:    store.PlanUsageRecord{},
			fiveHour: 90, sevenDay: 90, now: 3000,
		},
		{
			name:     "under both thresholds",
			usage:    usage,
			fiveHour: 95, sevenDay: 96, now: 3000,
		},
		{
			name:     "a window exactly at its threshold is over",
			usage:    usage,
			fiveHour: 92, sevenDay: 96, now: 3000,
			wantWindows: []string{PlanWindowFiveHour}, wantUntil: 5000, wantHolds: true,
		},
		{
			name:     "both over wake at the later reset",
			usage:    usage,
			fiveHour: 90, sevenDay: 90, now: 3000,
			wantWindows: []string{PlanWindowFiveHour, PlanWindowSevenDay}, wantUntil: 90000, wantHolds: true,
		},
		{
			name:     "a threshold of 0 is off",
			usage:    usage,
			fiveHour: 0, sevenDay: 0, now: 3000,
		},
		{
			name:     "a window past its reset counts as unused",
			usage:    usage,
			fiveHour: 90, sevenDay: 96, now: 5000,
		},
		{
			name:     "a Resume now holds off the cap it was pressed on",
			usage:    usage,
			fiveHour: 90, sevenDay: 96, resumedUntil: 5000, now: 3000,
			wantWindows: []string{PlanWindowFiveHour}, wantUntil: 5000,
		},
		{
			name:     "a window over past the hold caps again",
			usage:    usage,
			fiveHour: 90, sevenDay: 90, resumedUntil: 5000, now: 3000,
			wantWindows: []string{PlanWindowFiveHour, PlanWindowSevenDay}, wantUntil: 90000, wantHolds: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := capOf(test.usage, test.fiveHour, test.sevenDay, test.resumedUntil, test.now)
			if !slices.Equal(got.Windows, test.wantWindows) || got.Until != test.wantUntil {
				t.Fatalf("capOf = %v until %d; want %v until %d", got.Windows, got.Until, test.wantWindows, test.wantUntil)
			}
			if holds := got.Holds(test.now); holds != test.wantHolds {
				t.Fatalf("Holds = %v; want %v", holds, test.wantHolds)
			}
		})
	}
}

// TestPlanCapReadsItsSettings: the thresholds default to 90, a stored 0 is
// off, an unusable value falls back to the default, and a Resume now is
// stored, so a fresh PlanCap over the same settings still holds off.
func TestPlanCapReadsItsSettings(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	settings := db.Settings()

	if fiveHour, sevenDay := PlanCapThresholds(ctx, settings, nil); fiveHour != 90 || sevenDay != 90 {
		t.Fatalf("unset thresholds = %d, %d; want the defaults", fiveHour, sevenDay)
	}
	set := func(key, value string) {
		t.Helper()
		if err := settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	set(store.SettingPlanCapFiveHourPercent, "0")
	set(store.SettingPlanCapSevenDayPercent, "101")
	if fiveHour, sevenDay := PlanCapThresholds(ctx, settings, nil); fiveHour != 0 || sevenDay != 90 {
		t.Fatalf("thresholds = %d, %d; want 0 kept and 101 read as the default", fiveHour, sevenDay)
	}

	set(store.SettingPlanCapFiveHourPercent, "50")
	raw, _ := json.Marshal(store.PlanUsageRecord{
		FiveHour: &store.PlanUsageWindow{Utilization: 0.6, ResetsAt: 1 << 50},
		At:       1000,
	})
	set(store.SettingPlanUsage, string(raw))
	planCap := NewPlanCap(settings, nil)
	if err := planCap.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !planCap.Holds(2000) {
		t.Fatal("60% of the five-hour window against a 50% threshold does not cap")
	}
	if err := planCap.Resume(ctx, 2000); err != nil {
		t.Fatal(err)
	}
	if planCap.Holds(2000) {
		t.Fatal("the cap still holds after Resume now")
	}
	restarted := NewPlanCap(settings, nil)
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.Holds(2000) {
		t.Fatal("a restart forgot the Resume now")
	}
}
