// Package bus is a small in-process pub/sub used to fan events out to SSE
// clients and the Telegram mirror. Slow subscribers drop items rather than
// block producers.
package bus

import (
	"sync"
)

const (
	KindMessage    = "message"     // a chat message (human or loop) was persisted
	KindLoopStatus = "loop_status" // a loop's runtime state changed
	KindAgentEvent = "agent_event" // raw claude stdout event (incl. stream deltas)
	KindTurnResult = "turn_result" // a turn finished (cost/usage)
	KindSchedule   = "schedule"    // next_tick_at changed
	KindAccess     = "access"      // telegram sender allowlist changed
)

type Item struct {
	Kind    string `json:"kind"`
	LoopID  string `json:"loop_id,omitempty"`
	Payload any    `json:"payload"`
}

type subscriber struct {
	ch     chan Item
	filter func(Item) bool
}

type Bus struct {
	mu   sync.Mutex
	subs map[int]*subscriber
	next int
}

func New() *Bus {
	return &Bus{subs: map[int]*subscriber{}}
}

// Subscribe returns a channel of items matching filter (nil = all) and a
// cancel func. The channel is buffered; items are dropped if it fills.
func (b *Bus) Subscribe(filter func(Item) bool) (<-chan Item, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	sub := &subscriber{ch: make(chan Item, 256), filter: filter}
	b.subs[id] = sub
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
	}
	return sub.ch, cancel
}

func (b *Bus) Publish(item Item) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if s.filter != nil && !s.filter(item) {
			continue
		}
		select {
		case s.ch <- item:
		default: // drop for slow consumers
		}
	}
}
