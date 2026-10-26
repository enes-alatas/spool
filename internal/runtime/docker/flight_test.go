package docker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The read a caller starts belongs to every caller that joins it: when the
// starter's ctx ends mid-read, the starter gets its own ctx's error and
// the read goes on, uncancelled, to answer the caller still waiting
// (#1137). A sign-out's ctx ending must not fail the wakes that joined
// its wall check.
func TestFlightOutlivesTheCallerThatStartedIt(t *testing.T) {
	var shared flight[string]
	started, release := make(chan struct{}), make(chan struct{})
	readErr := make(chan error, 1)
	read := func(ctx context.Context) (string, error) {
		close(started)
		<-release
		readErr <- ctx.Err()
		return "answer", nil
	}

	starterCtx, cancelStarter := context.WithCancel(context.Background())
	starter := make(chan error, 1)
	go func() {
		_, err := shared.do(starterCtx, "key", read)
		starter <- err
	}()
	<-started

	type result struct {
		value string
		err   error
	}
	joinerCtx := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
	joiner := make(chan result, 1)
	go func() {
		value, err := shared.do(joinerCtx, "key", func(context.Context) (string, error) {
			t.Error("a caller that asked while the read ran started another")
			return "", nil
		})
		joiner <- result{value, err}
	}()
	<-joinerCtx.waiting

	cancelStarter()
	select {
	case err := <-starter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("starter got %v, want its own context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the starter waited out the read after its ctx ended")
	}

	close(release)
	got := <-joiner
	if got.err != nil || got.value != "answer" {
		t.Fatalf("joiner got (%q, %v), want the read's answer", got.value, got.err)
	}
	if err := <-readErr; err != nil {
		t.Fatalf("the read's ctx ended with the starter's: %v", err)
	}
}

// waitingContext tells, by closing waiting, that a caller has begun to
// wait on its Done: flight.do asks for it only once it has joined a read.
type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *waitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}
