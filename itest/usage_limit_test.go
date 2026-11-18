//go:build integration

package itest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// standingNote opens the note a resumed session gets when its standing
// instructions changed.
const standingNote = "[system note · your standing instructions changed]"

// A turn the plan's usage limit refuses holds the loop, capped, until the
// limit resets: no turn runs meanwhile, and what it is told waits. The
// turn after the reset carries the refused work and what came meanwhile,
// and a standing-instructions note at most once, as the instructions stand
// when it runs. A note the refused turn carried is in the session already,
// so it is not told again unless the instructions changed since (#1649,
// #1650).
func TestAUsageLimitHoldsTheLoopUntilItResets(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	if err := os.MkdirAll(hub.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	limited := filepath.Join(hub.fkState, "usage-limited")
	hub.createLoop("aster", nil)
	for _, name := range []string{"briar", "cedar", "dahlia"} {
		hub.createLoop(name, nil)
	}
	hub.mustJSON("POST", "/api/channels", map[string]any{"name": "ops"}, nil)
	hub.wantRefusal("PUT", "/api/channels/ops/loops/aster", nil, 204, "")
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	hub.waitState("aster", "asleep", 60*time.Second)

	// refuse, with a reset a few seconds off, a turn that owes a note
	limit := func(seconds string) {
		if err := os.WriteFile(limited, []byte(seconds), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	limit("4")
	hub.wantRefusal("PUT", "/api/channels/ops/loops/briar", nil, 204, "")
	sent := time.Now().UnixMilli()
	hub.message("aster", "first")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.IsError && tn.StartedAt >= sent })
	hub.waitState("aster", "capped", 10*time.Second)
	if until := hub.loop("aster").CappedUntil; until < sent+2000 || until > sent+10000 {
		t.Fatalf("capped_until = %d, want the limit's reset about 4s after %d", until, sent)
	}

	// held: what it is told waits, and the instructions change again
	hub.message("aster", "second")
	hub.wantRefusal("PUT", "/api/channels/ops/loops/cedar", nil, 204, "")
	if err := os.Remove(limited); err != nil {
		t.Fatal(err)
	}
	refused := len(hub.turns("aster"))
	time.Sleep(time.Second)
	if got := len(hub.turns("aster")); got != refused {
		t.Fatalf("aster ran %d turns while the limit held it, want none", got-refused)
	}
	after := hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError && tn.StartedAt >= sent })
	// ops as it stands after cedar joined during the hold, not as the
	// refused turn's note had it
	for _, want := range []string{"first", "second", "    channel:ops\n      loops: @briar, @cedar\n"} {
		if !strings.Contains(after.ResultText, want) {
			t.Errorf("the turn after the reset lacks %q:\n%s", want, after.ResultText)
		}
	}
	if notes := strings.Count(after.ResultText, standingNote); notes != 1 {
		t.Errorf("the turn after the reset carries the note %d times, want once:\n%s", notes, after.ResultText)
	}

	// a note the refused turn carried, with nothing changed since, is not
	// told again
	hub.waitState("aster", "asleep", 60*time.Second)
	limit("2")
	hub.wantRefusal("PUT", "/api/channels/ops/loops/dahlia", nil, 204, "")
	sent = time.Now().UnixMilli()
	hub.message("aster", "third")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.IsError && tn.StartedAt >= sent })
	if err := os.Remove(limited); err != nil {
		t.Fatal(err)
	}
	after = hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError && tn.StartedAt >= sent })
	if !strings.Contains(after.ResultText, "third") || strings.Contains(after.ResultText, standingNote) {
		t.Errorf("the turn after the second reset = %q, want third and no note", after.ResultText)
	}
}

// The usage limit is the login's: a loop moved to another Claude login
// while the limit holds it is no longer under it, and wakes with what
// waited, though the refusal named no reset: the operator moving it is
// their say that it may go, and the turn it then runs clears the
// usage_limit problem the refusal opened. The loop is bare, which the API
// refuses to put on a login, so the test names one in the store (#1649).
func TestALoopMovedOffALimitedLoginWakes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	hub := startServer(t, dataDir)
	hub.createLoop("aster", nil)
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	hub.stop()
	db := openStoreAt(t, dataDir)
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "ada", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE loops SET claude_login = 'ada' WHERE name = 'aster'`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	hub = startServer(t, dataDir)
	hub.waitState("aster", "asleep", 60*time.Second)

	if err := os.MkdirAll(hub.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	limited := filepath.Join(hub.fkState, "usage-limited")
	if err := os.WriteFile(limited, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UnixMilli()
	hub.message("aster", "first")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.IsError && tn.StartedAt >= sent })
	hub.waitState("aster", "capped", 10*time.Second)
	if err := os.Remove(limited); err != nil {
		t.Fatal(err)
	}
	hub.message("aster", "second")
	hub.noTurn(time.Second, map[string]string{"aster": "second"})

	hub.mustJSON("PATCH", "/api/loops/aster", map[string]any{"claude_login": "default"}, nil)
	after := hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError && tn.StartedAt >= sent })
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(after.ResultText, want) {
			t.Errorf("the turn after the move lacks %q:\n%s", want, after.ResultText)
		}
	}
	hub.waitState("aster", "asleep", 60*time.Second)
	if problems := hub.loop("aster").Problems; len(problems) != 0 {
		t.Errorf("problems after the turn = %+v, want the usage_limit one cleared", problems)
	}
}

// A refusal that names no reset holds the loop until the operator continues
// it: only they can tell when the limit has reset, so the hold opens the
// usage_limit problem, which tells the owner, and has no timer to lift it.
// Acknowledging the problem continues the loop with what waited (#1649).
func TestAUsageLimitWithNoResetWaitsForTheOperator(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	if err := os.MkdirAll(hub.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	limited := filepath.Join(hub.fkState, "usage-limited")
	hub.createLoop("aster", nil)
	hub.waitTurn("aster", firstTurnWait, func(turn) bool { return true })
	hub.waitState("aster", "asleep", 60*time.Second)

	if err := os.WriteFile(limited, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sent := time.Now().UnixMilli()
	hub.message("aster", "first")
	hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.IsError && tn.StartedAt >= sent })
	hub.waitState("aster", "capped", 10*time.Second)
	held := hub.loop("aster")
	if len(held.Problems) != 1 || held.Problems[0].Symptom != "usage_limit" || held.Health != "failing" || held.CappedUntil != 0 {
		t.Fatalf("held: problems %+v, health %q, capped_until %d; want usage_limit alone, failing, and no reset",
			held.Problems, held.Health, held.CappedUntil)
	}

	// the limit lifts, and nothing but the operator lets the loop go
	if err := os.Remove(limited); err != nil {
		t.Fatal(err)
	}
	hub.message("aster", "second")
	hub.noTurn(2*time.Second, map[string]string{"aster": "second"})

	hub.mustJSON("POST", "/api/loops/aster/problems/usage_limit/acknowledge", nil, nil)
	after := hub.waitTurn("aster", 30*time.Second, func(tn turn) bool { return !tn.IsError && tn.StartedAt >= sent })
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(after.ResultText, want) {
			t.Errorf("the turn after the operator continued it lacks %q:\n%s", want, after.ResultText)
		}
	}
}
