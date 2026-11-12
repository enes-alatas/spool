package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/enes-alatas/spool/internal/surface"
)

// Slack failing to give a join offer is kept as an offer is, so a slow or
// rate-limiting Slack is asked once a joinOfferLife, not at every read of a
// channel's view.
func TestAFailedJoinOfferIsKept(t *testing.T) {
	adapter, _, _ := inboundFixture(t)
	var asked atomic.Int32
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
	}))
	t.Cleanup(limited.Close)
	adapter.client = NewClientAt(limited.URL)

	for range 3 {
		if offer, err := adapter.JoinOffer(context.Background(), "loop_terra", "C0BACKEND"); err == nil {
			t.Fatalf("offer = %q with no error, want Slack's failure", offer)
		}
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("Slack was asked %d times for three reads, want once", got)
	}
	adapter.forgetOffers("loop_terra")
	if _, err := adapter.JoinOffer(context.Background(), "loop_terra", "C0BACKEND"); err == nil || asked.Load() != 2 {
		t.Errorf("after the loop changed, Slack was asked %d times in all, want it asked again", asked.Load())
	}
}

// An ask its caller gave up on mid-way says nothing of Slack, so it isn't
// kept: the next read asks again.
func TestAJoinOfferAskedForNoOneIsNotKept(t *testing.T) {
	adapter, _, _ := inboundFixture(t)
	var asked atomic.Int32
	arrived := make(chan struct{})
	release := make(chan struct{})
	answering := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked.Add(1) == 1 {
			close(arrived)
			<-release
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": map[string]any{"is_private": true}})
	}))
	t.Cleanup(answering.Close)
	t.Cleanup(func() { close(release) })
	adapter.client = NewClientAt(answering.URL)

	gone, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()
	if _, err := adapter.JoinOffer(gone, "loop_terra", "C0BACKEND"); err == nil {
		t.Fatal("an ask given up on mid-way answered with no error")
	}
	offer, err := adapter.JoinOffer(context.Background(), "loop_terra", "C0BACKEND")
	if err != nil || offer != surface.JoinPrivate {
		t.Fatalf("the next read = %q, %v, want private from Slack: the given-up ask was kept", offer, err)
	}
}
