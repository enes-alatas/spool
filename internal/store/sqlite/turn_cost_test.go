package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestCostBackfillRewritesHistory replays migration 0019 over rows shaped the
// way the bug wrote them — cost_usd holding the session's running total — and
// checks the history comes out priced per turn. The figures are the ones from
// the live store on #191: three small turns reading ~$9 each, and a second
// session starting over.
//
// The migration is re-run rather than reimplemented: the column is dropped and
// the version forgotten, so the file that ships is the thing under test.
func TestCostBackfillRewritesHistory(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.Loops().Create(ctx, testLoop()); err != nil {
		t.Fatal(err)
	}

	// Pre-migration shape: cost_usd is cumulative within a session, and an
	// errored turn repeats the previous figure rather than adding to it.
	rows := []struct {
		id, session string
		startedAt   int64
		cumulative  float64
	}{
		{"t1", "s1", 100, 8.75},
		{"t2", "s1", 200, 8.92},
		{"t3", "s1", 300, 9.08},
		{"t4", "s1", 400, 9.08}, // errored turn: spent nothing
		{"t5", "s2", 500, 0.29}, // new session, counter restarts
	}
	if err := unmigrate0019(db); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.db.Exec(
			`INSERT INTO turns (id, loop_id, session_id, started_at, ended_at, cost_usd)
			 VALUES (?,?,?,?,?,?)`,
			r.id, "l1", r.session, r.startedAt, r.startedAt+1, r.cumulative); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}

	want := map[string]struct{ cost, session float64 }{
		"t1": {8.75, 8.75}, // first of its session: the whole total is its own
		"t2": {0.17, 8.92},
		"t3": {0.16, 9.08},
		"t4": {0, 9.08},
		"t5": {0.29, 0.29},
	}
	for id, w := range want {
		var cost, session float64
		if err := db.db.QueryRow(
			`SELECT cost_usd, session_cost_usd FROM turns WHERE id=?`, id).Scan(&cost, &session); err != nil {
			t.Fatal(err)
		}
		if !nearly(cost, w.cost) || !nearly(session, w.session) {
			t.Errorf("%s: cost %v / session %v, want %v / %v", id, cost, session, w.cost, w.session)
		}
	}

	// The whole point: the column is now safe to sum.
	got, err := db.Turns().CostSince(ctx, "l1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := 8.75 + 0.17 + 0.16 + 0.29; !nearly(got, want) {
		t.Errorf("CostSince %v, want %v — the sum used to read %v", got, want, 8.75+8.92+9.08+9.08+0.29)
	}
}

// TestSessionCostIsTheSessionsHighest: a turn is priced against this, so it
// has to be the session's latest total and must ignore the in-flight turn
// asking, whose own column is still zero.
func TestSessionCostIsTheSessionsHighest(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.Loops().Create(ctx, testLoop()); err != nil {
		t.Fatal(err)
	}
	if got, err := db.Turns().SessionCost(ctx, "l1", "s1"); err != nil || got != 0 {
		t.Fatalf("unknown session: got %v, %v; want 0, nil", got, err)
	}

	for _, c := range []struct {
		id      string
		session float64
	}{{"t1", 0.40}, {"t2", 0.75}} {
		turn := &store.Turn{ID: c.id, LoopID: "l1", SessionID: "s1", StartedAt: 1}
		if err := db.Turns().Create(ctx, turn); err != nil {
			t.Fatal(err)
		}
		turn.EndedAt = 2
		turn.SessionCostUSD = c.session
		if err := db.Turns().Finish(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	// An in-flight turn contributes nothing to price itself against.
	if err := db.Turns().Create(ctx, &store.Turn{ID: "t3", LoopID: "l1", SessionID: "s1", StartedAt: 3}); err != nil {
		t.Fatal(err)
	}

	got, err := db.Turns().SessionCost(ctx, "l1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !nearly(got, 0.75) {
		t.Errorf("SessionCost %v, want the latest total 0.75", got)
	}
	if other, err := db.Turns().SessionCost(ctx, "l1", "s2"); err != nil || other != 0 {
		t.Errorf("a different session read %v, %v; want 0, nil", other, err)
	}
}

// unmigrate0019 returns the schema to its pre-0019 state so the shipped
// migration can be replayed over rows in the old shape. The index goes first:
// sqlite refuses to drop a column an index is built on.
func unmigrate0019(db *DB) error {
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_turns_session_cost`,
		`ALTER TABLE turns DROP COLUMN session_cost_usd`,
		`DELETE FROM schema_migrations WHERE version='0019_turn_cost_is_per_turn.sql'`,
	} {
		if _, err := db.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// TestCostBackfillPricesAgainstTheSessionHigh: a reported total that dips —
// an errored turn can come back with less than the session had reached — must
// not become the baseline for the turn after it. Pricing against the
// immediately preceding row did exactly that: the turn after the dip was
// charged for the whole session, which is the defect the migration repairs,
// reintroduced by the repair. Pricing against the session's high agrees with
// the engine's SessionCost by construction.
func TestCostBackfillPricesAgainstTheSessionHigh(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.Loops().Create(ctx, testLoop()); err != nil {
		t.Fatal(err)
	}
	if err := unmigrate0019(db); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id         string
		startedAt  int64
		cumulative float64
	}{
		{"t1", 100, 8.75},
		{"t2", 200, 8.92},
		{"t3", 300, 0}, // dipped
		{"t4", 400, 9.08},
	}
	for _, r := range rows {
		if _, err := db.db.Exec(
			`INSERT INTO turns (id, loop_id, session_id, started_at, ended_at, cost_usd)
			 VALUES (?,?,?,?,?,?)`,
			r.id, "l1", "s1", r.startedAt, r.startedAt+1, r.cumulative); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migrate(); err != nil {
		t.Fatal(err)
	}

	// t3 dipped, so it spent nothing; t4 gained 0.16 over the session's high
	// of 8.92, not the whole 9.08 over a baseline of zero.
	for id, want := range map[string]float64{"t1": 8.75, "t2": 0.17, "t3": 0, "t4": 0.16} {
		var cost float64
		if err := db.db.QueryRow(`SELECT cost_usd FROM turns WHERE id=?`, id).Scan(&cost); err != nil {
			t.Fatal(err)
		}
		if !nearly(cost, want) {
			t.Errorf("%s: cost %v, want %v", id, cost, want)
		}
	}
	got, err := db.Turns().CostSince(ctx, "l1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := 9.08; !nearly(got, want) {
		t.Errorf("CostSince %v, want the session's own total %v", got, want)
	}
}

// testLoop is the minimum a loop row's CHECK constraints accept.
func testLoop() *store.Loop {
	return &store.Loop{
		ID: "l1", Name: "alpha", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare,
	}
}

func nearly(got, want float64) bool {
	d := got - want
	return d < 0.0001 && d > -0.0001
}
