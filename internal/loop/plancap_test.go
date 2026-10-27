package loop

import (
	"context"
	"encoding/json"
	"errors"
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
	planCap := NewPlanCap(settings, db.ClaudeLogins(), nil)
	if err := planCap.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !planCap.Holds("", 2000) {
		t.Fatal("60% of the five-hour window against a 50% threshold does not cap")
	}
	if err := planCap.Resume(ctx, "", 2000); err != nil {
		t.Fatal(err)
	}
	if planCap.Holds("", 2000) {
		t.Fatal("the cap still holds after Resume now")
	}
	restarted := NewPlanCap(settings, db.ClaudeLogins(), nil)
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.Holds("", 2000) {
		t.Fatal("a restart forgot the Resume now")
	}
}

// Each Claude login has its own cap (ADR-0060): a login over a threshold
// caps the loops on it alone, a loop that names none follows the default's,
// and Resume now holds off one login's cap, stored where that login keeps
// it, so a restart keeps it.
func TestPlanCapIsJudgedPerLogin(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, name := range []string{"ada", "bloom"} {
		if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	usage := NewPlanUsage(db.Settings(), db.ClaudeLogins())
	for login, used := range map[string]float64{"": 0.2, "ada": 0.95, "bloom": 0.5} {
		if err := usage.Observe(ctx, login, store.PlanUsageRecord{
			FiveHour: &store.PlanUsageWindow{Utilization: used, ResetsAt: 1 << 50}, At: 1000, Source: "stream",
		}); err != nil {
			t.Fatal(err)
		}
	}
	planCap := NewPlanCap(db.Settings(), db.ClaudeLogins(), nil)
	if err := planCap.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for login, want := range map[string]bool{"": false, "default": false, "ada": true, "bloom": false, "gone": false} {
		if got := planCap.Holds(login, 2000); got != want {
			t.Errorf("the cap of %q holds %v; want %v", login, got, want)
		}
	}
	if got := planCap.At("ada", 2000); got.Until != 1<<50 || len(got.Windows) != 1 {
		t.Errorf("ada's cap = %+v; want five_hour until its reset", got)
	}

	if err := planCap.Resume(ctx, "ada", 2000); err != nil {
		t.Fatal(err)
	}
	if planCap.Holds("ada", 2000) {
		t.Fatal("ada's cap still holds after Resume now")
	}
	if _, err := db.Settings().Get(ctx, store.SettingPlanCapResumedUntil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Resume now on ada wrote the default's hold (%v)", err)
	}
	restarted := NewPlanCap(db.Settings(), db.ClaudeLogins(), nil)
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.Holds("ada", 2000) {
		t.Fatal("ada's Resume now did not survive a restart")
	}
}
