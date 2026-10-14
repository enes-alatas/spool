package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A loop has at most one open problem per symptom: opening it again while
// it is open changes nothing, clearing it closes it once, and the same
// symptom afterwards opens a new one (ADR-0053, #764).
func TestProblems(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	loop := &store.Loop{
		ID: "l1", Name: "ailing", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.Loops().Create(ctx, loop); err != nil {
		t.Fatal(err)
	}
	problems := db.Problems()
	must := func(changed bool, err error) bool {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	open := func() []store.Problem {
		t.Helper()
		got, err := problems.ListOpen(ctx, "l1")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if !must(problems.Open(ctx, "l1", store.SymptomTurnErrors, 10)) {
		t.Fatal("a first problem was not opened")
	}
	if must(problems.Open(ctx, "l1", store.SymptomTurnErrors, 20)) {
		t.Fatal("an open problem was opened again")
	}
	if !must(problems.Open(ctx, "l1", store.SymptomWorkstationDown, 15)) {
		t.Fatal("another symptom's problem was not opened")
	}
	if got := open(); len(got) != 2 || got[0] != (store.Problem{Symptom: store.SymptomTurnErrors, Since: 10}) ||
		got[1].Symptom != store.SymptomWorkstationDown {
		t.Fatalf("open = %+v, want turn_errors since 10, then workstation_down", got)
	}

	if !must(problems.Clear(ctx, "l1", store.SymptomTurnErrors, 30, store.ClearedByRecovery)) {
		t.Fatal("an open problem was not cleared")
	}
	if must(problems.Clear(ctx, "l1", store.SymptomTurnErrors, 31, store.ClearedByOperator)) {
		t.Fatal("a cleared problem was cleared again")
	}
	if !must(problems.Open(ctx, "l1", store.SymptomTurnErrors, 40)) {
		t.Fatal("the symptom recurring did not open a new problem")
	}
	if got := open(); len(got) != 2 || got[0].Symptom != store.SymptomWorkstationDown ||
		got[1] != (store.Problem{Symptom: store.SymptomTurnErrors, Since: 40}) {
		t.Fatalf("open = %+v, want workstation_down, then turn_errors since 40", got)
	}
}
