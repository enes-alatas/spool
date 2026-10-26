package loop

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// slowReaper is a runtime whose Reap takes a while, as a sandbox's CLI
// round trip does. It counts the reaps under way at once and every reap
// finished.
type slowReaper struct {
	runtime.Runtime
	mu               sync.Mutex
	inFlight, widest int
	reaped           map[string]bool
}

func (reaper *slowReaper) Reap(_ context.Context, loopID string, _ int) error {
	reaper.mu.Lock()
	reaper.inFlight++
	reaper.widest = max(reaper.widest, reaper.inFlight)
	reaper.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	reaper.mu.Lock()
	reaper.inFlight--
	reaper.reaped[loopID] = true
	reaper.mu.Unlock()
	return nil
}

// TestBootReapsSideBySide: boot recovery reaps its loops side by side, no
// more than maxReaping at once, and returns only once every reap is done,
// so no loop wakes before its orphan is gone (#1134). One after another,
// a hundred sandboxed reaps kept the hub's listeners down for seconds.
func TestBootReapsSideBySide(t *testing.T) {
	reaper := &slowReaper{reaped: map[string]bool{}}
	manager := &Manager{deps: Deps{Runtimes: map[string]runtime.Runtime{store.RuntimeDocker: reaper}}}
	var loops []*store.Loop
	for index := range 4 * maxReaping {
		loops = append(loops, &store.Loop{ID: fmt.Sprintf("loop-%d", index), Runtime: store.RuntimeDocker})
	}

	manager.reapOrphans(context.Background(), loops)

	if len(reaper.reaped) != len(loops) {
		t.Fatalf("reapOrphans returned with %d of %d loops reaped", len(reaper.reaped), len(loops))
	}
	if reaper.widest > maxReaping {
		t.Errorf("%d reaps ran at once, want at most %d", reaper.widest, maxReaping)
	}
	if reaper.widest < 2 {
		t.Errorf("reaps ran one after another")
	}
}
