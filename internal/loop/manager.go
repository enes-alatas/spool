package loop

import (
	"context"
	"fmt"
	"sync"
	"syscall"
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

// Boot recovers state after an orchestrator restart: kills orphan claude
// processes, closes dangling turns, and starts an actor per loop.
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
		if l.CurrentPID > 0 {
			killOrphan(l.CurrentPID)
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

// Remove shuts a loop's actor down (used on delete/archive).
func (m *Manager) Remove(id string) {
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

// killOrphan terminates a leftover claude process from a previous run.
// Pdeathsig should have handled it, but belt and braces.
func killOrphan(pid int) {
	proc, err := findProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(5 * time.Second)
		_ = proc.Signal(syscall.SIGKILL)
	}()
}

func findProcess(pid int) (*processHandle, error) {
	// only signal if the pid still exists and we own it
	if err := syscall.Kill(pid, 0); err != nil {
		return nil, err
	}
	return &processHandle{pid: pid}, nil
}

type processHandle struct{ pid int }

func (p *processHandle) Signal(sig syscall.Signal) error {
	if p.pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", p.pid)
	}
	return syscall.Kill(p.pid, sig)
}
