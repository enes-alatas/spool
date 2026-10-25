package loop

import (
	"errors"
	"testing"
	"time"
)

// A caller of an actor that has stopped, or stops while the call waits,
// gets its answer instead of waiting for good (#1113). The actor here never
// reads its commands, as a stopped one doesn't. Three moments: stopped
// before the call; stopped while the send waits on a full queue; and
// stopped after the command was queued but before anything answered it.
func TestNoCallerWaitsOnAStoppedActor(t *testing.T) {
	calls := map[string]func(actor *Actor) error{
		"pause":       func(actor *Actor) error { return actor.Pause() },
		"kill":        func(actor *Actor) error { return actor.Kill() },
		"rotate":      func(actor *Actor) error { return actor.Rotate("operator") },
		"rehome":      func(actor *Actor) error { return actor.Rehome() },
		"power":       func(actor *Actor) error { return actor.Power("restart") },
		"acknowledge": func(actor *Actor) error { actor.Acknowledge("turn_errors"); return nil },
		"deliver":     func(actor *Actor) error { actor.Deliver(Envelope{}); return nil },
		"tick":        func(actor *Actor) error { actor.Tick(); return nil },
		"update":      func(actor *Actor) error { actor.UpdateLoop(nil); return nil },
		"shutdown":    func(actor *Actor) error { actor.Shutdown(); return nil },
	}
	// a stopped actor has nothing left to cut off, so pause and kill
	// answer as a loop without one; the rest say it stopped
	wants := map[string]error{"rotate": ErrStopped, "rehome": ErrStopped, "power": ErrStopped}
	moments := map[string]int{"before the call": -1, "while the send waits": 0, "after the command was queued": 1}
	for name, call := range calls {
		for moment, queue := range moments {
			actor := &Actor{cmds: make(chan cmd, max(queue, 0)), stopped: make(chan struct{})}
			if queue < 0 {
				close(actor.stopped)
			} else {
				time.AfterFunc(20*time.Millisecond, func() { close(actor.stopped) })
			}
			answered := make(chan error, 1)
			go func() { answered <- call(actor) }()
			select {
			case err := <-answered:
				if !errors.Is(err, wants[name]) {
					t.Errorf("%s, the actor stopped %s = %v, want %v", name, moment, err, wants[name])
				}
			case <-time.After(5 * time.Second):
				t.Errorf("%s, the actor stopped %s: the call is still waiting", name, moment)
			}
		}
	}
}
