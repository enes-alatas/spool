package loop

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// Manager owns all loop actors and boot recovery.
type Manager struct {
	deps Deps

	mu       sync.RWMutex
	byID     map[string]*Actor
	nameToID map[string]string
}

func NewManager(deps Deps) *Manager {
	return &Manager{deps: deps, byID: map[string]*Actor{}, nameToID: map[string]string{}}
}

// Boot recovers state after an orchestrator restart: reaps claude processes
// left behind by the previous run, closes dangling turns, and starts an actor
// per loop. Reaping is the runtime's job — a bare loop has an orphan host
// process, a sandboxed one has a stale execution in a standing workstation.
func (manager *Manager) Boot(ctx context.Context) error {
	loops, err := manager.deps.Store.Loops().List(ctx)
	if err != nil {
		return err
	}
	nowMS := time.Now().UnixMilli()
	if err := manager.deps.Store.Turns().InterruptDangling(ctx, nowMS); err != nil {
		return err
	}
	manager.reapOrphans(ctx, loops)
	for _, loopRecord := range loops {
		if loopRecord.CurrentPID > 0 {
			_ = manager.deps.Store.Loops().SetRuntime(ctx, loopRecord.ID, loopRecord.CurrentSessionID, 0)
			loopRecord.CurrentPID = 0
		}
		if loopRecord.RotatePending && loopRecord.CurrentSessionID != "" {
			// The loop asked its session for a handoff note and the
			// orchestrator went down before the rotation landed. That
			// session's last turn was told it ends here, so it is retired
			// now rather than resumed; the note, if the turn produced one,
			// still seeds the fresh session (#66, ADR-0022).
			_ = manager.deps.Store.Sessions().End(ctx, loopRecord.CurrentSessionID, store.EndReasonRotated, nowMS)
			_ = manager.deps.Store.Loops().SetRuntime(ctx, loopRecord.ID, "", 0)
			_ = manager.deps.Store.Loops().SetRotation(ctx, loopRecord.ID, false, loopRecord.RotateReason, loopRecord.HandoffNote)
			manager.log().Info("rotation completed after restart", "loop", loopRecord.Name, "old_session", loopRecord.CurrentSessionID)
			loopRecord.CurrentSessionID = ""
			loopRecord.RotatePending = false
		}
		if rehomeDue(loopRecord) && loopRecord.CurrentSessionID == "" {
			// The rotation a rehome asked for landed, here or before the
			// restart, and the orchestrator went down before the move did.
			// The session that ran on the host is over, so the move is too.
			if left, err := rehome(ctx, &manager.deps, loopRecord); err != nil {
				manager.log().Error("rehome after restart", "loop", loopRecord.Name, "err", err)
			} else {
				manager.log().Info("rehomed after restart", "loop", loopRecord.Name, "left", left)
			}
		}
		if loopRecord.Status != store.StatusArchived {
			manager.add(loopRecord)
		}
	}
	return nil
}

func (manager *Manager) add(loopRecord *store.Loop) *Actor {
	actor := NewActor(manager.deps, loopRecord)
	manager.mu.Lock()
	manager.byID[loopRecord.ID] = actor
	manager.nameToID[loopRecord.Name] = loopRecord.ID
	manager.mu.Unlock()
	return actor
}

// Add registers and starts an actor for a newly created loop.
func (manager *Manager) Add(loopRecord *store.Loop) *Actor { return manager.add(loopRecord) }

// Remove shuts a loop's actor down and forgets it, ahead of the loop's
// delete: nothing of the loop runs once it returns. Its workstation is
// DestroyWorkstation's, after the loop's record, and the hub token with
// it, is gone.
func (manager *Manager) Remove(id string) {
	manager.mu.Lock()
	actor := manager.byID[id]
	delete(manager.byID, id)
	for name, lid := range manager.nameToID {
		if lid == id {
			delete(manager.nameToID, name)
		}
	}
	manager.mu.Unlock()
	if actor != nil {
		actor.Shutdown()
	}
}

// DestroyWorkstation powers a deleted loop's workstation off: a
// workstation outlives sleeps and pauses, never its loop. The caller
// names the loop's runtime kind, since the loop's record is gone.
func (manager *Manager) DestroyWorkstation(id, runtimeKind string) {
	if loopRuntime := manager.deps.runtimeFor(runtimeKind); loopRuntime == nil {
		manager.log().Warn("workstation power off", "loop", id, "err", fmt.Errorf("no %q runtime available", runtimeKind))
	} else if err := loopRuntime.Destroy(context.Background(), id); err != nil {
		manager.log().Warn("workstation power off", "loop", id, "err", err)
	}
}

func (manager *Manager) Get(id string) (*Actor, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	actor, ok := manager.byID[id]
	return actor, ok
}

func (manager *Manager) GetByName(name string) (*Actor, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	id, ok := manager.nameToID[name]
	if !ok {
		return nil, false
	}
	actor, ok := manager.byID[id]
	return actor, ok
}

// UpdateLoop pushes a fresh loop config into the actor (rename included).
func (manager *Manager) UpdateLoop(loopRecord *store.Loop) {
	manager.mu.Lock()
	for name, lid := range manager.nameToID {
		if lid == loopRecord.ID && name != loopRecord.Name {
			delete(manager.nameToID, name)
		}
	}
	manager.nameToID[loopRecord.Name] = loopRecord.ID
	actor := manager.byID[loopRecord.ID]
	manager.mu.Unlock()
	if actor != nil {
		actor.UpdateLoop(loopRecord)
	}
}

// Deliver routes an envelope to a loop by id; returns false if unknown.
func (manager *Manager) Deliver(id string, env Envelope) bool {
	actor, ok := manager.Get(id)
	if !ok {
		return false
	}
	actor.Deliver(env)
	return true
}

// GetFile reads a file the loop owns out of its workstation, for a message
// it sends with one (#123). Which files those are is the loop's runtime's
// to say.
func (manager *Manager) GetFile(ctx context.Context, loopRecord *store.Loop, path string, limit int64) ([]byte, error) {
	loopRuntime := manager.deps.runtimeFor(loopRecord.Runtime)
	if loopRuntime == nil {
		return nil, fmt.Errorf("no %q runtime available", loopRecord.Runtime)
	}
	return loopRuntime.GetFile(ctx, loopRecord.ID, loopRecord.WorkspacePath, path, limit)
}

func (manager *Manager) Tick(id string) bool {
	actor, ok := manager.Get(id)
	if !ok {
		return false
	}
	actor.Tick()
	return true
}

// CapChanged tells every loop that a Claude login's plan cap started or
// stopped holding (ADR-0047, ADR-0060): each asks after its own login's.
func (manager *Manager) CapChanged() {
	for _, actor := range manager.actors() {
		actor.CapChanged()
	}
}

// CloudGateChanged tells every loop that the public address was checked
// (ADR-0056). Only cloud loops act on it.
func (manager *Manager) CloudGateChanged() {
	for _, actor := range manager.actors() {
		actor.CloudGateChanged()
	}
}

// CloudHandOff tells the loop id how its cloud session's SessionStart post
// went (ADR-0059). An id with no actor is ignored.
func (manager *Manager) CloudHandOff(id string, answered bool) {
	if actor, ok := manager.Get(id); ok {
		actor.CloudHandOff(answered)
	}
}

// actors is every running actor, read under the lock and told outside it,
// since telling one blocks while its command queue is full.
func (manager *Manager) actors() []*Actor {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	actors := make([]*Actor, 0, len(manager.byID))
	for _, actor := range manager.byID {
		actors = append(actors, actor)
	}
	return actors
}

// Shutdown stops every actor; blocks until all claude processes are gone.
func (manager *Manager) Shutdown() {
	manager.mu.Lock()
	actors := make([]*Actor, 0, len(manager.byID))
	for _, actor := range manager.byID {
		actors = append(actors, actor)
	}
	manager.byID = map[string]*Actor{}
	manager.nameToID = map[string]string{}
	manager.mu.Unlock()
	var wg sync.WaitGroup
	for _, actor := range actors {
		wg.Add(1)
		go func(actor *Actor) {
			defer wg.Done()
			actor.Shutdown()
		}(actor)
	}
	wg.Wait()
}

func (manager *Manager) log() *slog.Logger {
	if manager.deps.Logger == nil {
		return slog.Default()
	}
	return manager.deps.Logger
}

// maxReaping is how many loops boot recovery reaps at once. A sandboxed
// reap asks the runtime's CLI over a round trip or two, so reaping a
// hundred loops one after another kept the listeners down for seconds
// (#1134); a bounded number side by side keeps the CLI's daemon from a
// hundred-call stampede.
const maxReaping = 16

// reapOrphans reaps every loop's leftover execution, maxReaping at a time,
// and returns once all are done: no loop wakes before its orphan is gone.
// A reap that fails is logged and boot goes on.
func (manager *Manager) reapOrphans(ctx context.Context, loops []*store.Loop) {
	reaping := make(chan struct{}, maxReaping)
	var reaps sync.WaitGroup
	for _, loopRecord := range loops {
		loopRuntime := manager.deps.runtimeFor(loopRecord.Runtime)
		if loopRuntime == nil {
			manager.log().Warn("reap orphan", "loop", loopRecord.Name, "err", fmt.Errorf("no %q runtime available", loopRecord.Runtime))
			continue
		}
		reaping <- struct{}{}
		reaps.Go(func() {
			defer func() { <-reaping }()
			if err := loopRuntime.Reap(ctx, loopRecord.ID, loopRecord.CurrentPID); err != nil {
				manager.log().Warn("reap orphan", "loop", loopRecord.Name, "err", err)
			}
		})
	}
	reaps.Wait()
}
