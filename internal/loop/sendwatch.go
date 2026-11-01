package loop

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/surface"
)

// The send_failed outage guard (#1144, the constraint of 2026-09-18). Failed
// sends from outageLoops loops or more on one surface within outageWindow
// mean the surface is down: their problems still open, but the hub holds
// each loop's wake and its owner's notice, which would go out over the same
// broken surface, until a send on that surface gets through.
const (
	outageLoops  = 3
	outageWindow = 5 * time.Minute
)

// sendOutages follows send outcomes per surface: which loops failed on it
// lately, whether it is down, and the surface each loop last failed on.
type sendOutages struct {
	mu       sync.Mutex
	failures map[string]map[string]int64 // surface → loop → its latest failure there, Unix ms
	down     map[string]bool
	lastOn   map[string]string // loop → the surface of its latest failure
}

func newSendOutages() *sendOutages {
	return &sendOutages{failures: map[string]map[string]int64{}, down: map[string]bool{}, lastOn: map[string]string{}}
}

// note takes one outcome at at, and reports whether it started an outage
// or ended one.
func (outages *sendOutages) note(outcome *surface.SendOutcome, at int64) (started, ended bool) {
	if outcome.Surface == "" {
		return false, false // a dismissal says nothing about the surface
	}
	outages.mu.Lock()
	defer outages.mu.Unlock()
	if !outcome.Failed {
		// Only an outage that has started ends here: before one does, the
		// count runs on, or a surface whose sends land now and then would
		// never trip the guard.
		if !outages.down[outcome.Surface] {
			return false, false
		}
		delete(outages.down, outcome.Surface)
		delete(outages.failures, outcome.Surface)
		return false, true
	}
	if outcome.LoopID == "" {
		return false, false
	}
	outages.lastOn[outcome.LoopID] = outcome.Surface
	recent := outages.failures[outcome.Surface]
	if recent == nil {
		recent = map[string]int64{}
		outages.failures[outcome.Surface] = recent
	}
	recent[outcome.LoopID] = at
	for loopID, failedAt := range recent {
		if at-failedAt > outageWindow.Milliseconds() {
			delete(recent, loopID)
		}
	}
	if len(recent) >= outageLoops && !outages.down[outcome.Surface] {
		outages.down[outcome.Surface] = true
		return true, false
	}
	return false, false
}

// holds reports whether the surface the loop last failed on is down.
func (outages *sendOutages) holds(loopID string) bool {
	outages.mu.Lock()
	defer outages.mu.Unlock()
	surfaceName, ok := outages.lastOn[loopID]
	return ok && outages.down[surfaceName]
}

// WatchSends follows the surfaces' send outcomes until ctx is done: each
// tells the loop that sent it to look at its undelivered sends again, and a
// send that gets through on a surface that was down tells every loop, so
// the ones whose wake and notice it held have them now (#1144).
func (manager *Manager) WatchSends(ctx context.Context) {
	outcomes, cancel := manager.deps.Bus.SubscribeLossless(func(item bus.Item) bool { return item.Kind == bus.KindSendOutcome })
	defer cancel()
	log := manager.deps.Logger
	if log == nil {
		log = slog.Default()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-outcomes:
			if !ok {
				return
			}
			outcome, ok := item.Payload.(*surface.SendOutcome)
			if !ok {
				continue
			}
			started, ended := manager.outages.note(outcome, now())
			switch {
			case started:
				log.Warn("surface looks down: its owner notices and wakes for undelivered sends are held", "surface", outcome.Surface)
			case ended:
				log.Info("surface is up again: held undelivered-send notices go out", "surface", outcome.Surface)
				for _, actor := range manager.actors() {
					actor.SendsChanged()
				}
				continue
			}
			if outcome.LoopID == "" {
				continue
			}
			if actor, ok := manager.Get(outcome.LoopID); ok {
				actor.SendsChanged()
			}
		}
	}
}

// SendsChanged tells the loop to look at its undelivered sends again: one
// failed, got through, or was dismissed, or a surface came back (#1144).
// It never waits: one watcher tells every loop, and an actor whose commands
// have backed up must not hold the rest. A loop that misses it looks again
// at its next turn's end.
func (actor *Actor) SendsChanged() {
	select {
	case actor.cmds <- cmd{kind: "sends"}:
	default:
	}
}
