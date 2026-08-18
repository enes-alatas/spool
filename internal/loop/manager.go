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
func (m *Manager) Boot(ctx context.Context) error {
	loops, err := m.deps.Store.Loops().List(ctx)
	if err != nil {
		return err
	}
	nowMS := time.Now().UnixMilli()
	if err := m.deps.Store.Turns().InterruptDangling(ctx, nowMS); err != nil {
		return err
	}
	for _, l := range loops {
		if loopRuntime := m.deps.runtimeFor(l.Runtime); loopRuntime == nil {
			m.log().Warn("reap orphan", "loop", l.Name, "err", fmt.Errorf("no %q runtime available", l.Runtime))
		} else if err := loopRuntime.Reap(ctx, l.ID, l.CurrentPID); err != nil {
			m.log().Warn("reap orphan", "loop", l.Name, "err", err)
		}
		if l.CurrentPID > 0 {
			_ = m.deps.Store.Loops().SetRuntime(ctx, l.ID, l.CurrentSessionID, 0)
			l.CurrentPID = 0
		}
		if l.Status != store.StatusArchived {
			m.add(l)
		}
	}
	return nil
}

func (m *Manager) add(l *store.Loop) *Actor {
	actor := NewActor(m.deps, l)
	m.mu.Lock()
	m.byID[l.ID] = actor
	m.nameToID[l.Name] = l.ID
	m.mu.Unlock()
	return actor
}

// Add registers and starts an actor for a newly created loop.
func (m *Manager) Add(l *store.Loop) *Actor { return m.add(l) }

// Remove shuts a loop's actor down and powers its workstation off (used on
// delete: a workstation outlives sleeps and pauses, never its loop). The
// caller names the loop's runtime kind — the loop record may already be
// deleted by the time this runs.
func (m *Manager) Remove(id, runtimeKind string) {
	m.mu.Lock()
	actor := m.byID[id]
	delete(m.byID, id)
	for name, lid := range m.nameToID {
		if lid == id {
			delete(m.nameToID, name)
		}
	}
	m.mu.Unlock()
	if actor != nil {
		actor.Shutdown()
	}
	if loopRuntime := m.deps.runtimeFor(runtimeKind); loopRuntime == nil {
		m.log().Warn("workstation power off", "loop", id, "err", fmt.Errorf("no %q runtime available", runtimeKind))
	} else if err := loopRuntime.PowerOff(context.Background(), id); err != nil {
		m.log().Warn("workstation power off", "loop", id, "err", err)
	}
}

func (m *Manager) Get(id string) (*Actor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	actor, ok := m.byID[id]
	return actor, ok
}

func (m *Manager) GetByName(name string) (*Actor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.nameToID[name]
	if !ok {
		return nil, false
	}
	actor, ok := m.byID[id]
	return actor, ok
}

// UpdateLoop pushes a fresh loop config into the actor (rename included).
func (m *Manager) UpdateLoop(l *store.Loop) {
	m.mu.Lock()
	for name, lid := range m.nameToID {
		if lid == l.ID && name != l.Name {
			delete(m.nameToID, name)
		}
	}
	m.nameToID[l.Name] = l.ID
	actor := m.byID[l.ID]
	m.mu.Unlock()
	if actor != nil {
		actor.UpdateLoop(l)
	}
}

// Deliver routes an envelope to a loop by id; returns false if unknown.
func (m *Manager) Deliver(id string, env Envelope) bool {
	actor, ok := m.Get(id)
	if !ok {
		return false
	}
	actor.Deliver(env)
	return true
}

func (m *Manager) Tick(id string) bool {
	actor, ok := m.Get(id)
	if !ok {
		return false
	}
	actor.Tick()
	return true
}

// Shutdown stops every actor; blocks until all claude processes are gone.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	actors := make([]*Actor, 0, len(m.byID))
	for _, actor := range m.byID {
		actors = append(actors, actor)
	}
	m.byID = map[string]*Actor{}
	m.nameToID = map[string]string{}
	m.mu.Unlock()
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

func (m *Manager) log() *slog.Logger {
	if m.deps.Logger == nil {
		return slog.Default()
	}
	return m.deps.Logger
}
