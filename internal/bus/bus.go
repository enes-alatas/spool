// Package bus is a small in-process pub/sub used to fan events out to SSE
// clients and the surface mirrors. Nothing blocks producers: a slow
// subscriber drops items, and a lossless one queues them.
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
	KindAccess      = "access"      // a sender allowlist changed, on any surface
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
	// failing anything (ADR-0029).
	KindSendRetry = "send_retry"
	// KindClaudeLogin says a loop's Claude login was refused, or ran again
	// after a refusal (#419). Its payload is a surface.LoginNotice, and every
	// surface adapter must deliver it to the loop's owner (ADR-0029).
	// The refusal is published once per outage of each loop, not once per
	// refused retry.
	KindClaudeLogin = "claude_login"
	// KindReaction says a reaction on a message was added or removed
	// (ADR-0040). Its payload is a route.ReactionPayload. A reaction is not
	// a message, so it is never published as KindMessage: the control room
	// would draw it as one said. Every surface adapter that mirrors outward
	// sets a loop's reaction from it (ADR-0029).
	KindReaction = "reaction"
	// KindPoll says a poll's ballot changed after it was sent: a vote, or
	// the close (ADR-0041). Its payload is a route.PollPayload. The poll
	// itself went out as KindMessage; a vote is not a message, so it is
	// never published as one. Every surface adapter that mirrors outward
	// updates or stops the platform's poll from it (ADR-0029).
	KindPoll = "poll"
)

type Item struct {
	Kind    string `json:"kind"`
	LoopID  string `json:"loop_id,omitempty"`
	Payload any    `json:"payload"`
}

type subscriber struct {
	ch     chan Item
	filter func(Item) bool
	// lossless subscribers queue what their channel cannot take yet in
	// pending, and forward it from a goroutine of their own, rather than
	// drop it; done ends that goroutine.
	lossless bool
	mu       sync.Mutex
	pending  []Item
	wake     chan struct{}
	done     chan struct{}
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
func (bus *Bus) Subscribe(filter func(Item) bool) (<-chan Item, func()) {
	return bus.subscribe(&subscriber{ch: make(chan Item, 256), filter: filter})
}

// SubscribeLossless is Subscribe for a consumer that must see every item,
// such as a surface mirror, for which a dropped loop send is a message lost
// without a record (#302). What its channel cannot take yet waits in memory
// instead of being dropped, so Publish still never blocks. The consumer
// must keep reading: nothing bounds the backlog of one that stops.
func (bus *Bus) SubscribeLossless(filter func(Item) bool) (<-chan Item, func()) {
	sub := &subscriber{ch: make(chan Item, 256), filter: filter, lossless: true,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go sub.forward()
	return bus.subscribe(sub)
}

func (bus *Bus) subscribe(sub *subscriber) (<-chan Item, func()) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	id := bus.next
	bus.next++
	bus.subs[id] = sub
	cancel := func() {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		if registered, ok := bus.subs[id]; ok {
			delete(bus.subs, id)
			if registered.lossless {
				// the forwarder owns the channel, and closes it
				close(registered.done)
			} else {
				close(registered.ch)
			}
		}
	}
	return sub.ch, cancel
}

func (bus *Bus) Publish(item Item) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, sub := range bus.subs {
		if sub.filter != nil && !sub.filter(item) {
			continue
		}
		if sub.lossless {
			sub.queue(item)
			continue
		}
		select {
		case sub.ch <- item:
		default: // drop for slow consumers
		}
	}
}

// queue holds item for a lossless subscriber's forwarder.
func (sub *subscriber) queue(item Item) {
	sub.mu.Lock()
	sub.pending = append(sub.pending, item)
	sub.mu.Unlock()
	select {
	case sub.wake <- struct{}{}:
	default: // a wake is already due
	}
}

// forward hands a lossless subscriber's queued items to its channel, in
// the order they were published, until the subscription is cancelled.
func (sub *subscriber) forward() {
	defer close(sub.ch)
	for {
		sub.mu.Lock()
		batch := sub.pending
		sub.pending = nil
		sub.mu.Unlock()
		for _, item := range batch {
			select {
			case sub.ch <- item:
			case <-sub.done:
				return
			}
		}
		select {
		case <-sub.wake:
		case <-sub.done:
			return
		}
	}
}
