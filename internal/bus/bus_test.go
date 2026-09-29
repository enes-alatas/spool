package bus

import (
	"testing"
	"time"
)

// A plain subscriber that does not keep up loses items; a lossless one gets
// every item, in order, however far behind it falls (#302).
func TestSubscribeLosslessKeepsEveryItem(t *testing.T) {
	publisher := New()
	plain, cancelPlain := publisher.Subscribe(nil)
	defer cancelPlain()
	lossless, cancelLossless := publisher.SubscribeLossless(nil)
	defer cancelLossless()

	const n = 1000
	for i := range n {
		publisher.Publish(Item{Kind: KindMessage, Payload: i})
	}
	if got := len(plain); got >= n {
		t.Fatalf("the plain subscriber holds %d items, want some dropped", got)
	}
	for want := range n {
		select {
		case item := <-lossless:
			if item.Payload != want {
				t.Fatalf("item %d is %v: out of order", want, item.Payload)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the lossless subscriber got %d of %d items", want, n)
		}
	}
}

// Cancelling a lossless subscription closes its channel, with items still
// queued behind it.
func TestSubscribeLosslessCancelCloses(t *testing.T) {
	publisher := New()
	items, cancel := publisher.SubscribeLossless(nil)
	for i := range 500 {
		publisher.Publish(Item{Kind: KindMessage, Payload: i})
	}
	cancel()
	publisher.Publish(Item{Kind: KindMessage}) // no longer subscribed
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-items:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the channel of a cancelled lossless subscription never closed")
		}
	}
}
