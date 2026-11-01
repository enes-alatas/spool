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

// missedWakeHub is a loop "gardener" with an actor that runs no goroutine,
// the way one that has stopped answering looks from outside, in a manager
// whose wake check has a grace of a minute (#1143). Its notices are the
// problem notices told.
func missedWakeHub(t *testing.T, st store.Store) (*Manager, *Actor, <-chan bus.Item) {
	t.Helper()
	ctx := context.Background()
	loopRecord := store.Loop{
		ID: "l1", Name: "gardener", Mission: "m", Model: "haiku", Status: store.StatusActive,
		WorkspaceMode: store.WorkspaceNone, Pacing: store.PacingFixed,
		Runtime: store.RuntimeBare, CreatedAt: 1, UpdatedAt: 1,
	}
	if err := st.Loops().Create(ctx, &loopRecord); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().Set(ctx, store.SettingHealthWakeGraceMinutes, "1"); err != nil {
		t.Fatal(err)
	}
	hub := bus.New()
	notices, cancel := hub.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindLoopProblem })
	t.Cleanup(cancel)
	deps := Deps{Store: st, Bus: hub}
	actor := &Actor{deps: deps, loop: loopRecord, cmds: make(chan cmd, 32), stopped: make(chan struct{})}
	actor.stateSnap.Store(StateAsleep)
	actor.loadProblems()
	manager := &Manager{deps: deps, byID: map[string]*Actor{"l1": actor}, nameToID: map[string]string{"gardener": "l1"}}
	return manager, actor, notices
}

func openTestStore(t *testing.T) store.Store {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// nextNotice is the next problem notice told, failing the test after a
// while with none.
func nextNotice(t *testing.T, notices <-chan bus.Item) *surface.ProblemNotice {
	t.Helper()
	select {
	case item := <-notices:
		return item.Payload.(*surface.ProblemNotice)
	case <-time.After(5 * time.Second):
		t.Fatal("no problem notice was told")
		return nil
	}
}

func noNotice(t *testing.T, notices <-chan bus.Item, why string) {
	t.Helper()
	select {
	case item := <-notices:
		t.Fatalf("%s, yet the owner was told %+v", why, item.Payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// Work waiting in the inbox is owed a turn only while no turn runs and
// nothing holds the loop. A reaction that only wakes it is not, nor is
// the crash ladder's wait, whose tries are turns (#1143).
func TestNoteOwed(t *testing.T) {
	message := Envelope{Text: "hello"}
	wakeOnly := Envelope{}
	cases := []struct {
		name  string
		setup func(actor *Actor)
		owed  bool
	}{
		{"a message waits", func(actor *Actor) { actor.inbox = []Envelope{message} }, true},
		{"nothing waits", func(actor *Actor) {}, false},
		{"only a reaction waits", func(actor *Actor) { actor.inbox = []Envelope{wakeOnly} }, false},
		{"a turn runs", func(actor *Actor) { actor.inbox, actor.turn = []Envelope{message}, &store.Turn{} }, false},
		{"the crash ladder waits", func(actor *Actor) { actor.inbox, actor.backoff = []Envelope{message}, backoffStart }, false},
		{"the workstation is down", func(actor *Actor) { actor.inbox, actor.wsDown = []Envelope{message}, true }, false},
		{"the loop is paused", func(actor *Actor) { actor.inbox, actor.paused = []Envelope{message}, true }, false},
	}
	for _, testCase := range cases {
		actor := &Actor{deps: Deps{Store: openTestStore(t)}}
		testCase.setup(actor)
		actor.noteOwed()
		if owed := actor.owedSnap.Load() != 0; owed != testCase.owed {
			t.Errorf("%s: owed = %v, want %v", testCase.name, owed, testCase.owed)
		}
	}

	actor := &Actor{inbox: []Envelope{message}}
	actor.owedSnap.Store(1)
	actor.noteOwed()
	if since := actor.owedSnap.Load(); since != 1 {
		t.Fatalf("work still waiting is owed since %d, want since it began", since)
	}
}

// Work the actor read and holds in its inbox, past the grace with nothing
// holding the loop, opens wake_missed once, dated from when it was owed. A
// turn starting clears it as a recovery (#1143).
func TestWorkWaitingPastTheGraceOpensWakeMissed(t *testing.T) {
	manager, actor, notices := missedWakeHub(t, openTestStore(t))
	owedAt := now() - 2*time.Minute.Milliseconds()
	actor.owedSnap.Store(owedAt)

	manager.CheckWakes()
	notice := nextNotice(t, notices)
	if !notice.Opened || notice.Symptom != store.SymptomWakeMissed || notice.Since != owedAt || notice.LoopName != "gardener" {
		t.Fatalf("told %+v, want wake_missed opened for gardener since %d", notice, owedAt)
	}
	if !actor.hasProblem(store.SymptomWakeMissed) {
		t.Fatalf("open problems = %+v, want wake_missed", actor.Problems())
	}
	manager.CheckWakes()
	noNotice(t, notices, "the problem was open already")

	actor.turnStartedAt.Store(now())
	actor.clearMissedWake()
	if notice := nextNotice(t, notices); notice.Opened || notice.ByOperator {
		t.Fatalf("a turn starting told %+v, want a recovery", notice)
	}
}

// No wake_missed opens for a wake within its grace, one a turn started
// after, or a loop held by something the owner hears of or chose (#1143).
func TestAWakeAnsweredOrHeldOpensNothing(t *testing.T) {
	owedAt := now() - 2*time.Minute.Milliseconds()
	cases := []struct {
		name  string
		setup func(actor *Actor)
	}{
		{"within the grace", func(actor *Actor) { actor.owedSnap.Store(now()) }},
		{"a turn started since", func(actor *Actor) { actor.owedSnap.Store(owedAt); actor.turnStartedAt.Store(owedAt + 1) }},
		{"busy", func(actor *Actor) { actor.owedSnap.Store(owedAt); actor.stateSnap.Store(StateBusy) }},
		{"paused", func(actor *Actor) { actor.owedSnap.Store(owedAt); actor.stateSnap.Store(StatePaused) }},
		{"capped", func(actor *Actor) { actor.owedSnap.Store(owedAt); actor.stateSnap.Store(StateCapped) }},
	}
	for _, testCase := range cases {
		manager, actor, notices := missedWakeHub(t, openTestStore(t))
		testCase.setup(actor)
		manager.CheckWakes()
		noNotice(t, notices, testCase.name)
		if open := actor.Problems(); len(open) != 0 {
			t.Fatalf("%s: open problems = %+v, want none", testCase.name, open)
		}
	}
}

// startsTurnOnOpen is a store whose problems' Open runs then, with the row
// stored and nobody told yet.
type startsTurnOnOpen struct {
	store.Store
	then func()
}

func (st startsTurnOnOpen) Problems() store.ProblemStore {
	return problemsThen{st.Store.Problems(), st.then}
}

type problemsThen struct {
	store.ProblemStore
	then func()
}

func (problems problemsThen) Open(ctx context.Context, loopID, symptom string, since int64) (bool, error) {
	opened, err := problems.ProblemStore.Open(ctx, loopID, symptom, since)
	problems.then()
	return opened, err
}

// A turn that starts, or a pause that comes, while the wake check stores
// its row withdraws the problem before the owner is told: the check sees
// it once the row is in, or the turn's own clear sees the row (#1143).
func TestAWakeAnsweredWhileTheCheckOpensIsWithdrawn(t *testing.T) {
	for _, change := range []string{"turn", "pause"} {
		var actor *Actor
		st := startsTurnOnOpen{Store: openTestStore(t)}
		st.then = func() {
			if change == "turn" {
				actor.turnStartedAt.Store(now())
			} else {
				actor.stateSnap.Store(StatePaused)
			}
		}
		manager, built, notices := missedWakeHub(t, st)
		actor = built
		actor.owedSnap.Store(now() - 2*time.Minute.Milliseconds())

		manager.CheckWakes()
		noNotice(t, notices, "a "+change+" came as the row was stored")
		if open := actor.Problems(); len(open) != 0 {
			t.Fatalf("after a %s as the row was stored, open problems = %+v, want none", change, open)
		}
	}
}

// An actor that has stopped reading its commands still owes the wakes
// asked of it, and acknowledging the problem they open neither waits on
// it nor sends it anything. The grace starts over (#1143).
func TestAcknowledgingAMissedWakeDoesNotWaitOnTheActor(t *testing.T) {
	manager, actor, notices := missedWakeHub(t, openTestStore(t))
	actor.Tick()
	asked := now() - 2*time.Minute.Milliseconds()
	actor.wakesMu.Lock()
	actor.wakesAskedAt = asked
	actor.wakesMu.Unlock()

	manager.CheckWakes()
	if notice := nextNotice(t, notices); !notice.Opened || notice.Since != asked {
		t.Fatalf("an unread tick told %+v, want wake_missed opened since %d", notice, asked)
	}

	acknowledged := make(chan bool, 1)
	go func() { acknowledged <- actor.AcknowledgeMissedWake("l1", "gardener") }()
	select {
	case cleared := <-acknowledged:
		if !cleared {
			t.Fatal("the acknowledge found no problem open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledge waited on an actor that reads nothing")
	}
	if notice := nextNotice(t, notices); notice.Opened || !notice.ByOperator {
		t.Fatalf("the acknowledge told %+v, want cleared by the operator", notice)
	}
	if open := actor.Problems(); len(open) != 0 {
		t.Fatalf("open problems = %+v, want none", open)
	}
	if since := actor.wakeOwedSince(); since <= asked {
		t.Fatalf("the unread tick is owed since %d after the acknowledge, want its grace started over", since)
	}
	if len(actor.cmds) != 1 {
		t.Fatalf("the actor has %d commands waiting, want the tick alone", len(actor.cmds))
	}
	manager.CheckWakes()
	noNotice(t, notices, "the grace started over")
}
