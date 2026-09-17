//go:build integration

package itest

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Ported from scripts/e2e/m2.sh (#3): the scheduler's contract, run against
// fakeclaude so CI keeps it rather than a milestone script nobody runs.

// TestPauseHoldsWorkUntilResume: a paused loop stops ticking, and work sent
// to it waits instead of waking it. Resume releases both — the queued message
// is delivered, not dropped, which is what makes pause safe to use on a loop
// mid-conversation.
func TestPauseHoldsWorkUntilResume(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("restive", map[string]any{"tick_interval_sec": 2})

	// let the creation tick land, so what follows is the paused loop's doing
	s.waitTurn("restive", 30*time.Second, func(turn) bool { return true })
	s.mustJSON("POST", "/api/loops/restive/pause", nil, nil)
	if next := s.loop("restive").NextTickAt; next != 0 {
		t.Fatalf("pause left a tick scheduled at %d", next)
	}
	before := len(s.completed("restive"))

	s.message("restive", "wait for me")
	// three tick intervals: enough that a loop still ticking would show it
	time.Sleep(6 * time.Second)
	if after := len(s.completed("restive")); after != before {
		t.Fatalf("a paused loop ran %d turns", after-before)
	}
	if state := s.loop("restive").State; state != "paused" {
		t.Fatalf("paused loop reports state %q", state)
	}

	s.mustJSON("POST", "/api/loops/restive/resume", nil, nil)
	answered := s.waitTurn("restive", 30*time.Second, func(tr turn) bool {
		return strings.Contains(tr.ResultText, "wait for me")
	})
	if answered.Trigger != "message" {
		t.Fatalf("the held message came back as trigger %q", answered.Trigger)
	}
}

// TestKillThenWakeResumesTheSameSession: killing a loop's process is not the
// same as losing its session. The next wake resumes where it was — the kill
// is a way to stop a turn, not to make the loop forget.
func TestKillThenWakeResumesTheSameSession(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("interruptible", nil)

	first := s.waitTurn("interruptible", 30*time.Second, func(turn) bool { return true })
	s.mustJSON("POST", "/api/loops/interruptible/kill", nil, nil)
	s.waitState("interruptible", "asleep", 30*time.Second)

	s.mustJSON("POST", "/api/loops/interruptible/wake", nil, nil)
	woken := s.waitTurn("interruptible", 30*time.Second, func(tr turn) bool {
		return tr.ID != first.ID && tr.StartedAt > first.StartedAt
	})
	if woken.SessionID != first.SessionID {
		t.Fatalf("wake after kill started session %s; the session survives a killed process (was %s)",
			woken.SessionID, first.SessionID)
	}
}

// TestOverdueTickFiresSoonAfterRestart: a loop whose tick came due while the
// orchestrator was down must not wait a fresh interval for it. Boot pulls
// every overdue tick into the next minute — spread by jitter so a whole fleet
// does not spawn at once (internal/sched) — and this pins that the schedule
// is rewritten to the jitter window rather than pushed an interval out.
func TestOverdueTickFiresSoonAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	// a long interval, so a tick scheduled the ordinary way is hours out and
	// cannot be mistaken for the overdue one being pulled forward
	s.createLoop("overdue", map[string]any{"tick_interval_sec": 7200})
	s.waitTurn("overdue", 30*time.Second, func(turn) bool { return true })
	s.waitState("overdue", "asleep", 30*time.Second)
	s.stop()

	// the tick that was due two hours out is now in the past
	setNextTick(t, dataDir, "overdue", time.Now().Add(-time.Hour).UnixMilli())

	s2 := startServer(t, dataDir)
	deadline := time.Now().Add(15 * time.Second)
	for {
		delta := time.Until(time.UnixMilli(s2.loop("overdue").NextTickAt))
		if delta > 0 && delta <= 70*time.Second {
			return // pulled into the jitter window, not pushed an interval out
		}
		if time.Now().After(deadline) {
			t.Fatalf("overdue tick rescheduled %s out; boot must pull it into the next minute", delta)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// setNextTick rewrites a loop's scheduled tick directly in spool.db, which is
// how a test makes a tick overdue without waiting for one: the API has no way
// to say "this was due an hour ago", and sleeping through a real interval
// would cost the suite a minute per assertion. The server must be stopped —
// its own writes would race this one.
func setNextTick(t *testing.T, dataDir, name string, at int64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Exec(
		`UPDATE schedule SET next_tick_at=? WHERE loop_id=(SELECT id FROM loops WHERE name=?)`, at, name)
	if err != nil {
		t.Fatalf("overdue tick for %s: %v", name, err)
	}
	// a typo in the name would otherwise pass as a test that proves nothing
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("overdue tick for %s touched %d schedule rows, want 1 (%v)", name, n, err)
	}
}
