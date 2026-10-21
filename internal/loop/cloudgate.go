package loop

import (
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// CloudCheckFresh is how long a passed check of the public address lets
// cloud loops wake. A wake after that waits for the address to be checked
// again, so every wake follows a check that passed moments before it
// (ADR-0056).
const CloudCheckFresh = time.Minute

// CloudGate is what the hub's checks of its public address say about
// waking a cloud loop. Every cloud loop shares the one address, so the
// gate is the fleet's: it holds them all while the last check failed,
// while none has run, once the last pass is older than CloudCheckFresh,
// while the address stored is not the one that passed, and while the rest
// of Settings → Cloud isn't set up: no account is signed in, or no
// environment is chosen (ADR-0059). Cloud loops fail closed (ADR-0056).
type CloudGate struct {
	// address reads the public address as stored now, "" for none, and
	// setUp whether an account is signed in and an environment chosen.
	address func() (string, error)
	setUp   func() (bool, error)
	recheck chan struct{} // asks the checker for a check; holds at most one

	mu sync.Mutex
	// passedAt is when the last check passed, in Unix ms, 0 when it failed
	// or none has run.
	passedAt int64
	// passedFor is the address the last check passed on.
	passedFor string
	// failedSince is when the checks started failing, in Unix ms, 0 while
	// the last one passed or none has run.
	failedSince int64
	onChange    func()
}

// NewCloudGate returns a gate that holds until a check first passes. It
// reads the address it guards with address, and with setUp whether an
// account is signed in and an environment chosen.
func NewCloudGate(address func() (string, error), setUp func() (bool, error)) *CloudGate {
	return &CloudGate{address: address, setUp: setUp, recheck: make(chan struct{}, 1)}
}

// OnChange sets what Record calls after each check.
func (gate *CloudGate) OnChange(notify func()) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.onChange = notify
}

// Record takes a check of address, made at the time at in Unix ms, and
// tells OnChange. passed is false for a check that failed. A check that
// could not run, since no address is set, is neither: it is recorded with
// Clear.
func (gate *CloudGate) Record(address string, passed bool, at int64) {
	gate.mu.Lock()
	if passed {
		gate.passedAt, gate.passedFor, gate.failedSince = at, address, 0
	} else {
		gate.passedAt, gate.passedFor = 0, ""
		if gate.failedSince == 0 {
			gate.failedSince = at
		}
	}
	notify := gate.onChange
	gate.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// SetupChanged tells OnChange that an account was signed in or removed,
// or an environment chosen or cleared, which holds or lets go every cloud
// loop as a check does.
func (gate *CloudGate) SetupChanged() {
	gate.mu.Lock()
	notify := gate.onChange
	gate.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Clear records that there is no address to check: cloud loops are held,
// and no check has failed.
func (gate *CloudGate) Clear() {
	gate.mu.Lock()
	gate.passedAt, gate.passedFor, gate.failedSince = 0, "", 0
	notify := gate.onChange
	gate.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// Holds reports whether a cloud loop must not wake at the time now, in Unix
// ms. A gate held only because its pass has gone stale, or was on another
// address than the one stored now, asks for a check, so the wake it holds
// goes ahead once one passes. One held by a failed check, or by no
// address, waits for the next scheduled check or the page's: asking again
// on every held wake would only repeat the failure, and each check's
// OnChange asks the loops again. Setup unfinished, or an address or setup
// that can't be read, holds without asking.
func (gate *CloudGate) Holds(now int64) bool {
	if setUp, err := gate.setUp(); err != nil || !setUp {
		return true
	}
	address, err := gate.address()
	if err != nil {
		return true
	}
	gate.mu.Lock()
	passedAt := gate.passedAt
	gate.mu.Unlock()
	if passedAt == 0 {
		return true
	}
	if gate.PassedFor(address, now) {
		return false
	}
	gate.Recheck()
	return true
}

// PassedFor reports whether the last check passed on address within
// CloudCheckFresh of now. The wake asks it of the address it is about to
// hand the session, so one changed since Holds isn't used.
func (gate *CloudGate) PassedFor(address string, now int64) bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.passedAt != 0 && address != "" && address == gate.passedFor &&
		now-gate.passedAt < CloudCheckFresh.Milliseconds()
}

// FailedSince is when the checks started failing, in Unix ms, 0 while
// they are not.
func (gate *CloudGate) FailedSince() int64 {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.failedSince
}

// Recheck asks for a check of the address now, without waiting for it.
func (gate *CloudGate) Recheck() {
	select {
	case gate.recheck <- struct{}{}:
	default: // a check is already asked for
	}
}

// Rechecks is where the checker hears Recheck.
func (gate *CloudGate) Rechecks() <-chan struct{} { return gate.recheck }

// Passed reports whether the last check passed, however long ago.
func (gate *CloudGate) Passed() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.passedAt != 0
}

// cloudHeld reports whether the loop is a cloud loop its public address
// holds. Like the cap it is asked at every boundary, so a pass gone stale
// holds the next turn until the address is checked again.
func (actor *Actor) cloudHeld() bool {
	return actor.loop.Runtime == store.RuntimeCloud && actor.deps.CloudGate != nil &&
		actor.deps.CloudGate.Holds(now())
}

// cloudGateChanged follows a check of the public address. A failed one
// opens the cloud loop's cloud_address problem, dated from the first
// failure, and one that passes clears it; with no address set there is
// nothing to say either way. A check that lets the loop go wakes it with
// whatever waited meanwhile, or with a tick when one was dropped and
// nothing else waits.
func (actor *Actor) cloudGateChanged() {
	if actor.loop.Runtime != store.RuntimeCloud || actor.deps.CloudGate == nil {
		return
	}
	if since := actor.deps.CloudGate.FailedSince(); since != 0 {
		actor.openProblem(store.SymptomCloudAddress, since, 0)
	} else if actor.deps.CloudGate.Passed() {
		actor.clearProblem(store.SymptomCloudAddress, store.ClearedByRecovery)
	}
	if actor.holdsWork() {
		return
	}
	actor.drainStoredInbox()
	if actor.cloudTickDropped && len(actor.inbox) == 0 && (actor.state == StateAsleep || actor.state == StateIdle) {
		actor.enqueue(TickEnvelope(time.Now(), &actor.loop))
	}
	actor.cloudTickDropped = false
	actor.publishState()
	actor.pump()
}
