package docker

import (
	"context"
	"sync"
)

// flight shares a docker read among the callers that ask for it while it
// runs: they take its answer instead of queueing to repeat it. A burst of
// wakes, as a message to the fleet channel makes, then pays for each read
// once rather than once per loop (#1133). A caller that asks once a read
// has ended starts a new one: nothing is kept past the read.
type flight[Answer any] struct {
	mu    sync.Mutex
	calls map[string]*flightCall[Answer]
}

// flightCall is one read under way: value and err are set before done
// closes.
type flightCall[Answer any] struct {
	done  chan struct{}
	value Answer
	err   error
}

// do runs read for key, or joins the one already running for it. The read
// belongs to no one caller: it runs under ctx's values but not its
// cancellation, bounded by the timeouts of the docker calls it makes. A
// caller whose ctx ends gives up alone, the one that started the read
// included, and the read goes on for the callers still waiting.
func (shared *flight[Answer]) do(ctx context.Context, key string, read func(context.Context) (Answer, error)) (Answer, error) {
	shared.mu.Lock()
	call, ok := shared.calls[key]
	if !ok {
		call = &flightCall[Answer]{done: make(chan struct{})}
		if shared.calls == nil {
			shared.calls = map[string]*flightCall[Answer]{}
		}
		shared.calls[key] = call
		go shared.run(context.WithoutCancel(ctx), key, call, read)
	}
	shared.mu.Unlock()
	select {
	case <-call.done:
		return call.value, call.err
	case <-ctx.Done():
		var zero Answer
		return zero, ctx.Err()
	}
}

// run makes call's read and hands its answer to every caller waiting.
func (shared *flight[Answer]) run(ctx context.Context, key string, call *flightCall[Answer], read func(context.Context) (Answer, error)) {
	call.value, call.err = read(ctx)
	shared.mu.Lock()
	delete(shared.calls, key)
	shared.mu.Unlock()
	close(call.done)
}
