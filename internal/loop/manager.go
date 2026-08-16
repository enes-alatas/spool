package loop

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// Manager owns all loop runtimes and boot recovery.
type Manager struct {
	deps Deps

	mu       sync.RWMutex
	byID     map[string]*Runtime
	nameToID map[string]string
}

func NewManager(deps Deps) *Manager {
	return &Manager{deps: deps, byID: map[string]*Runtime{}, nameToID: map[string]string{}}
}

// Boot recovers state after an orchestrator restart: kills orphan claude
// processes, closes dangling turns, and starts a runtime per loop.
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

func (m *Manager) add(l *store.Loop) *Runtime {
	rt := NewRuntime(m.deps, l)
	m.mu.Lock()
	m.byID[l.ID] = rt
	m.nameToID[l.Name] = l.ID
	m.mu.Unlock()
	return rt
}

// Add registers and starts a runtime for a newly created loop.
func (m *Manager) Add(l *store.Loop) *Runtime { return m.add(l) }

// Remove shuts a loop's runtime down (used on delete/archive).
func (m *Manager) Remove(id string) {
	m.mu.Lock()
	rt := m.byID[id]
	delete(m.byID, id)
	for name, lid := range m.nameToID {
		if lid == id {
			delete(m.nameToID, name)
		}
	}
	m.mu.Unlock()
	if rt != nil {
		rt.Shutdown()
	}
}

func (m *Manager) Get(id string) (*Runtime, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.byID[id]
	return rt, ok
}

func (m *Manager) GetByName(name string) (*Runtime, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.nameToID[name]
	if !ok {
		return nil, false
	}
	rt, ok := m.byID[id]
	return rt, ok
}

// UpdateLoop pushes a fresh loop config into the runtime (rename included).
func (m *Manager) UpdateLoop(l *store.Loop) {
	m.mu.Lock()
	for name, lid := range m.nameToID {
		if lid == l.ID && name != l.Name {
			delete(m.nameToID, name)
		}
	}
	m.nameToID[l.Name] = l.ID
	rt := m.byID[l.ID]
	m.mu.Unlock()
	if rt != nil {
		rt.UpdateLoop(l)
	}
}

// Deliver routes an envelope to a loop by id; returns false if unknown.
func (m *Manager) Deliver(id string, env Envelope) bool {
	rt, ok := m.Get(id)
	if !ok {
		return false
	}
	rt.Deliver(env)
	return true
}

func (m *Manager) Tick(id string) bool {
	rt, ok := m.Get(id)
	if !ok {
		return false
	}
	rt.Tick()
	return true
}

// Shutdown stops every runtime; blocks until all claude processes are gone.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	rts := make([]*Runtime, 0, len(m.byID))
	for _, rt := range m.byID {
		rts = append(rts, rt)
	}
	m.byID = map[string]*Runtime{}
	m.nameToID = map[string]string{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, rt := range rts {
		wg.Add(1)
		go func(rt *Runtime) {
			defer wg.Done()
			rt.Shutdown()
		}(rt)
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
