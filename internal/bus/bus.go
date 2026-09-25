// Package bus is a small in-process pub/sub used to fan events out to SSE
// clients and the Telegram mirror. Slow subscribers drop items rather than
// block producers.
package bus

import (
	"sync"
)

const (
	KindMessage     = "message"     // a chat message (human or loop) was persisted
	KindLoopStatus  = "loop_status" // a loop's runtime state changed
	KindAgentEvent  = "agent_event" // raw claude stdout event (incl. stream deltas)
	KindTurnResult  = "turn_result" // a turn finished (cost/usage)
	KindSchedule    = "schedule"    // next_tick_at changed
	KindAccess      = "access"      // telegram sender allowlist changed
	KindWorkstation = "workstation" // a loop's workstation liveness changed
	KindModels      = "models"      // the model list or a resolution on it changed
	// KindSendRetry asks the surfaces to send an already-persisted message
	// again, after the operator retried a failed send (#269). It carries
	// the same payload as KindMessage and is a separate kind precisely so
	// the control room does not treat it as one: the message was persisted
	// and rendered once already, and re-publishing it as KindMessage would
	// show the operator a second copy of what they are trying to un-lose.
	//
	// Every surface adapter must mirror it alongside KindMessage; one that
	// subscribes to KindMessage alone drops the operator's retries without
	// failing anything (ADR-0029 item 2).
	KindSendRetry = "send_retry"
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
