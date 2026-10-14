package loop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
	"github.com/enes-alatas/spool/internal/surface"
)

// Health is failing while the workstation is down, warning for errored
// turns alone, and ok with nothing open (ADR-0053).
func TestHealth(t *testing.T) {
	down := store.Problem{Symptom: store.SymptomWorkstationDown}
	failing := store.Problem{Symptom: store.SymptomTurnErrors}
	cases := []struct {
		problems []store.Problem
		want     string
	}{
		{nil, HealthOK},
		{[]store.Problem{failing}, HealthWarning},
		{[]store.Problem{down}, HealthFailing},
		{[]store.Problem{failing, down}, HealthFailing},
	}
	for _, testCase := range cases {
		if got := Health(testCase.problems); got != testCase.want {
			t.Errorf("Health(%+v) = %q, want %q", testCase.problems, got, testCase.want)
		}
	}
}

// Only a workstation that is up clears its workstation_down problem as a
// recovery. A verdict that stops the grace period without being up, such
// as a lost workstation judged never built after a hub restart, or a
// refused login, leaves the problem open and tells the owner nothing:
// they would read that the workstation is up again (#764).
func TestOnlyAnUpWorkstationClearsItsProblem(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	loopRecord := store.Loop{
		ID: "l1", Name: "ailing", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.Loops().Create(ctx, &loopRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Problems().Open(ctx, "l1", store.SymptomWorkstationDown, 1); err != nil {
		t.Fatal(err)
	}
	hub := bus.New()
	notices, cancel := hub.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindLoopProblem })
	defer cancel()
	actor := &Actor{deps: Deps{Store: db, Bus: hub}, loop: loopRecord, downTimer: time.NewTimer(time.Hour)}
	actor.downTimer.Stop()
	actor.loadProblems()

	for _, reason := range []string{DownReasonNotProvisioned, DownReasonUnauthenticated} {
		actor.setWorkstationDown(reason, "")
		if open := actor.Problems(); len(open) != 1 {
			t.Fatalf("after a %s verdict the open problems are %+v, want workstation_down still open", reason, open)
		}
	}
	select {
	case item := <-notices:
		t.Fatalf("a verdict that is not up told the owner %+v", item.Payload)
	case <-time.After(200 * time.Millisecond):
	}

	actor.setWorkstationUp()
	select {
	case item := <-notices:
		if notice := item.Payload.(*surface.ProblemNotice); notice.Opened || notice.ByOperator {
			t.Fatalf("an up verdict told %+v, want a recovery", notice)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an up verdict told the owner nothing")
	}
	if open := actor.Problems(); len(open) != 0 {
		t.Fatalf("after an up verdict the open problems are %+v, want none", open)
	}
}
