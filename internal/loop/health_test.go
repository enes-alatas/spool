package loop

import (
	"context"
	"path/filepath"
	"strconv"
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

	manager.CheckHealth()
	notice := nextNotice(t, notices)
	if !notice.Opened || notice.Symptom != store.SymptomWakeMissed || notice.Since != owedAt || notice.LoopName != "gardener" {
		t.Fatalf("told %+v, want wake_missed opened for gardener since %d", notice, owedAt)
	}
	if !actor.hasProblem(store.SymptomWakeMissed) {
		t.Fatalf("open problems = %+v, want wake_missed", actor.Problems())
	}
	manager.CheckHealth()
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
		manager.CheckHealth()
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

		manager.CheckHealth()
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

	manager.CheckHealth()
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
	manager.CheckHealth()
	noNotice(t, notices, "the grace started over")
}

// An actor that reads nothing holds none of its senders once its command
// buffer is full: the scheduler's tick pass is one sender for every loop,
// and waiting there stopped every loop's ticks (#1259). The commands it
// was sent meanwhile reach it in the order sent once it reads again, and
// the ticks among them stay owed until then.
func TestASendToAFullActorWaitsForNothing(t *testing.T) {
	_, actor, _ := missedWakeHub(t, openTestStore(t))
	const sent = 40 // past the buffer's 32
	sendsWithoutWaiting(t, func() {
		for i := range sent {
			actor.Tick()
			actor.UpdateLoop(&store.Loop{Name: strconv.Itoa(i)})
		}
	})
	actor.wakesMu.Lock()
	asked := actor.wakesAsked
	actor.wakesMu.Unlock()
	if asked != sent {
		t.Fatalf("%d ticks owed, want the %d sent", asked, sent)
	}

	for i := range sent {
		for _, want := range []string{"tick", "update"} {
			select {
			case command := <-actor.cmds:
				if command.kind != want || (want == "update" && command.loop.Name != strconv.Itoa(i)) {
					t.Fatalf("command %d read as %q %+v, want %q of loop %d: out of order", i, command.kind, command.loop, want, i)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("command %d never reached the actor", i)
			}
		}
	}
	select {
	case command := <-actor.cmds:
		t.Fatalf("an extra %q command reached the actor", command.kind)
	case <-time.After(100 * time.Millisecond):
	}
}

// An actor that stops with commands still queued for it drops them as it
// drops its unread buffer, and the wakes among them are owed no longer:
// nothing will ever read them (#1259).
func TestAnActorThatStopsSettlesTheWakesQueuedForIt(t *testing.T) {
	_, actor, _ := missedWakeHub(t, openTestStore(t))
	sendsWithoutWaiting(t, func() {
		for range 40 {
			actor.Tick()
		}
	})
	close(actor.stopped)
	within := time.Now().Add(5 * time.Second)
	for {
		actor.wakesMu.Lock()
		asked := actor.wakesAsked
		actor.wakesMu.Unlock()
		if asked == cap(actor.cmds) {
			break
		}
		if time.Now().After(within) {
			t.Fatalf("%d ticks owed after the stop, want only the %d in the buffer", asked, cap(actor.cmds))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if actor.send(cmd{kind: "tick"}) {
		t.Fatal("a send to a stopped actor was taken")
	}
}

// sendsWithoutWaiting runs sends, failing the test should they wait on an
// actor that reads nothing.
func sendsWithoutWaiting(t *testing.T, sends func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sends()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a send waited on an actor that reads nothing")
	}
}

// A turn past its limit is caught outside the actor, so an actor that has
// stopped answering mid-turn shows turn_stuck as a slow turn does; the
// actor here reads nothing at all (#1260). The problem is dated from the
// turn's start and told once; acknowledged, it doesn't open again for the
// same turn, and the next turn takes a new limit.
func TestATurnPastItsLimitIsCaughtOutsideTheActor(t *testing.T) {
	st := openTestStore(t)
	manager, actor, notices := missedWakeHub(t, st)
	if err := st.Settings().Set(context.Background(), store.SettingHealthTurnMinutes, "1"); err != nil {
		t.Fatal(err)
	}
	started := now() - 3*time.Minute.Milliseconds()
	actor.watchTurn(&store.Turn{ID: "t1", StartedAt: started})

	manager.CheckHealth()
	notice := nextNotice(t, notices)
	if !notice.Opened || notice.Symptom != store.SymptomTurnStuck || notice.Since != started || notice.Minutes != 3 {
		t.Fatalf("a turn past its limit told %+v, want turn_stuck opened since %d after 3 minutes", notice, started)
	}
	manager.CheckHealth()
	noNotice(t, notices, "the problem is open")

	if !actor.AcknowledgeStuckTurn("l1", "gardener") {
		t.Fatal("the acknowledge found no problem open")
	}
	if notice := nextNotice(t, notices); notice.Opened || !notice.ByOperator {
		t.Fatalf("the acknowledge told %+v, want cleared by the operator", notice)
	}
	manager.CheckHealth()
	noNotice(t, notices, "the operator acknowledged the turn that is still running")

	actor.unwatchTurn()
	manager.CheckHealth()
	noNotice(t, notices, "no turn runs")
	next := now() - 2*time.Minute.Milliseconds()
	actor.watchTurn(&store.Turn{ID: "t2", StartedAt: next})
	manager.CheckHealth()
	if notice := nextNotice(t, notices); !notice.Opened || notice.Since != next {
		t.Fatalf("the next turn past its limit told %+v, want turn_stuck opened since %d", notice, next)
	}
	actor.unwatchTurn()
	if notice := nextNotice(t, notices); notice.Opened {
		t.Fatalf("the turn's end told %+v, want turn_stuck cleared", notice)
	}
}

// A turn within its limit opens nothing, and the limit a turn takes is the
// one standing as it starts: the setting lowered meanwhile, or turned off,
// doesn't reach the running turn (#1142, #1260).
func TestATurnKeepsTheLimitItStartedWith(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	manager, actor, notices := missedWakeHub(t, st)
	if err := st.Settings().Set(ctx, store.SettingHealthTurnMinutes, "5"); err != nil {
		t.Fatal(err)
	}
	actor.watchTurn(&store.Turn{ID: "t1", StartedAt: now() - 3*time.Minute.Milliseconds()})
	if err := st.Settings().Set(ctx, store.SettingHealthTurnMinutes, "1"); err != nil {
		t.Fatal(err)
	}
	manager.CheckHealth()
	noNotice(t, notices, "the turn is within the limit it started with")

	if err := st.Settings().Set(ctx, store.SettingHealthTurnMinutes, "0"); err != nil {
		t.Fatal(err)
	}
	actor.unwatchTurn()
	actor.watchTurn(&store.Turn{ID: "t2", StartedAt: now() - 3*time.Minute.Milliseconds()})
	manager.CheckHealth()
	noNotice(t, notices, "the limit was off as the turn started")
}
