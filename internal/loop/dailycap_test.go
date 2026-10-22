package loop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// A day is its calendar date in its own zone, so the day a DST change
// falls on is 23 or 25 hours long, not 24.
func TestLocalDayFollowsTheCalendarAcrossDST(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no zone database:", err)
	}
	for _, row := range []struct {
		date  time.Time
		hours time.Duration
	}{
		{time.Date(2026, 3, 29, 12, 0, 0, 0, berlin), 23},
		{time.Date(2026, 10, 25, 12, 0, 0, 0, berlin), 25},
		{time.Date(2026, 10, 22, 23, 59, 0, 0, berlin), 24},
	} {
		start, next := LocalDay(row.date)
		if start.Hour() != 0 || start.Day() != row.date.Day() || next.Sub(start) != row.hours*time.Hour {
			t.Errorf("LocalDay(%s) = %s to %s, want midnight to midnight, %d hours", row.date, start, next, row.hours)
		}
	}
}

// The loop's daily cap counts what its turns cost since the hub's local
// day started, holds until the next day at or over the cap, and holds
// nothing with no cap, or once the spend is a day old (#652).
func TestTheDailyCapCountsTodaysTurns(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	loopRecord := store.Loop{
		ID: "l1", Name: "frugal", Mission: "m", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, DailyCapUSD: 1, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.Loops().Create(ctx, &loopRecord); err != nil {
		t.Fatal(err)
	}
	start, next := LocalDay(time.Now())
	spend := func(id string, at time.Time, usd float64) {
		turn := &store.Turn{ID: id, LoopID: "l1", SessionID: "s1", Trigger: store.TriggerTick, StartedAt: at.UnixMilli()}
		if err := db.Turns().Create(ctx, turn); err != nil {
			t.Fatal(err)
		}
		turn.EndedAt, turn.CostUSD = at.UnixMilli(), &usd
		if err := db.Turns().Finish(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	actor := &Actor{deps: Deps{Store: db}, loop: loopRecord}

	spend("yesterday", start.Add(-time.Minute), 5)
	spend("today", start.Add(time.Second), 0.6)
	if until := actor.dailyCapUntil(); until != 0 {
		t.Fatalf("0.6 today and 5 yesterday under a cap of 1 holds until %d, want no hold", until)
	}
	spend("again", start.Add(2*time.Second), 0.4)
	if until := actor.dailyCapUntil(); until != 0 {
		t.Fatalf("the cap held before it read the new turn: %d", until)
	}
	actor.spendStale = true // as a turn's end marks it
	if until := actor.dailyCapUntil(); until != next.UnixMilli() {
		t.Fatalf("1.0 today under a cap of 1 holds until %d, want the next local day at %d", until, next.UnixMilli())
	}
	actor.spendDay = start.Add(-24 * time.Hour).UnixMilli() // as if the spend were read yesterday
	actor.spendToday = 0
	if until := actor.dailyCapUntil(); until != next.UnixMilli() {
		t.Fatalf("a new day did not read the spend again: holds until %d", until)
	}
	actor.dailyCapTimer = time.NewTimer(time.Hour)
	actor.dailyCapTimer.Stop()
	actor.armDailyCap() // as the timer firing early, under a clock set back, does
	if !actor.dailyCapTimer.Stop() || !actor.wasCapped {
		t.Fatal("a cap that still holds left the day's timer unarmed")
	}

	uncapped := &Actor{deps: Deps{Store: db}, loop: loopRecord, spendStale: true}
	uncapped.loop.DailyCapUSD = 0
	if until := uncapped.dailyCapUntil(); until != 0 {
		t.Errorf("no cap at 1.0 today holds until %d, want no hold", until)
	}
}
