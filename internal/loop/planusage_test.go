package loop

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	usage := NewPlanUsage(db.Settings(), db.ClaudeLogins())

	if record, err := LastPlanUsage(ctx, db.Settings()); err != nil || record.At != 0 {
		t.Fatalf("before any observation = %+v, %v; want the zero record", record, err)
	}
	observe := func(record store.PlanUsageRecord) {
		t.Helper()
		if err := usage.Observe(ctx, "", record); err != nil {
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
	usage := NewPlanUsage(db.Settings(), db.ClaudeLogins())
	fiveHour := func(utilization float64, resetsAt, at int64) *store.PlanUsageWindow {
		t.Helper()
		if err := usage.Observe(ctx, "", store.PlanUsageRecord{
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

// Each Claude login keeps its own plan usage (ADR-0060): an observation on
// one login neither raises nor lowers another's, a loop that names none
// reports into the default's, a removed login keeps nothing, and the list
// gives the default first, the rest by name.
func TestPlanUsageIsKeptPerLogin(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, name := range []string{"bloom", "ada"} {
		if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: name, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	usage := NewPlanUsage(db.Settings(), db.ClaudeLogins())
	observe := func(login string, utilization float64, at int64) {
		t.Helper()
		if err := usage.Observe(ctx, login, store.PlanUsageRecord{
			FiveHour: &store.PlanUsageWindow{Utilization: utilization, ResetsAt: 1 << 50}, At: at, Source: "stream",
		}); err != nil {
			t.Fatal(err)
		}
	}
	observe("ada", 0.9, 1000)
	observe("", 0.2, 2000)
	observe("bloom", 0.1, 3000)
	observe("default", 0.3, 4000)
	observe("gone", 0.99, 5000)

	usages, err := PlanUsages(ctx, db.Settings(), db.ClaudeLogins())
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		login string
		used  float64
	}{{"default", 0.3}, {"ada", 0.9}, {"bloom", 0.1}}
	if len(usages) != len(want) {
		t.Fatalf("usages = %+v; want %v", usages, want)
	}
	for i, row := range want {
		if got := usages[i]; got.Login != row.login || got.Usage.FiveHour == nil || got.Usage.FiveHour.Utilization != row.used {
			t.Errorf("usages[%d] = %s %+v; want %s at %v", i, got.Login, got.Usage.FiveHour, row.login, row.used)
		}
	}
	if _, err := PlanUsageOf(ctx, db.Settings(), db.ClaudeLogins(), "gone"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a login the hub doesn't hold reads %v; want ErrNotFound", err)
	}
}

// Until the cap works per login (#1075), any login over a threshold caps
// the fleet, until the latest reset among the logins over.
func TestPlanCapHoldsWhileAnyLoginIsOver(t *testing.T) {
	usages := []LoginPlanUsage{
		{Login: "default", Usage: store.PlanUsageRecord{FiveHour: &store.PlanUsageWindow{Utilization: 0.2, ResetsAt: 9000}}},
		{Login: "ada", Usage: store.PlanUsageRecord{FiveHour: &store.PlanUsageWindow{Utilization: 0.95, ResetsAt: 5000}}},
		{Login: "bloom", Usage: store.PlanUsageRecord{
			FiveHour: &store.PlanUsageWindow{Utilization: 0.91, ResetsAt: 7000},
			SevenDay: &store.PlanUsageWindow{Utilization: 0.5, ResetsAt: 80000},
		}},
	}
	planCap := fleetCapOf(usages, 90, 90, 0, 1000)
	if !planCap.Holds(1000) || !slices.Equal(planCap.Windows, []string{PlanWindowFiveHour}) || planCap.Until != 7000 {
		t.Fatalf("cap = %+v; want five_hour until 7000, bloom's reset", planCap)
	}
	if planCap := fleetCapOf(usages, 90, 90, 0, 7000); planCap.Holds(7000) {
		t.Fatalf("cap at 7000 = %+v; want lifted once both logins over have reset", planCap)
	}
}
