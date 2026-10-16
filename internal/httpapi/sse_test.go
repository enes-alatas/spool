package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
)

// actingClient is a stream's response writer that, like a client acting
// the moment it sees the stream open, runs act when the headers are first
// flushed.
type actingClient struct {
	*httptest.ResponseRecorder
	mu    sync.Mutex
	acted bool
	act   func()
}

func (client *actingClient) Flush() {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.ResponseRecorder.Flush()
	if !client.acted {
		client.acted = true
		client.act()
	}
}

func (client *actingClient) Write(p []byte) (int, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.ResponseRecorder.Write(p)
}

func (client *actingClient) body() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.Body.String()
}

// TestAStreamHearsWhatIsPublishedOnceItIsOpen: an item published as soon
// as the stream's headers reach the client reaches the stream (#902).
func TestAStreamHearsWhatIsPublishedOnceItIsOpen(t *testing.T) {
	events := bus.New()
	client := &actingClient{ResponseRecorder: httptest.NewRecorder()}
	client.act = func() { events.Publish(bus.Item{Kind: bus.KindMessage}) }
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/api/stream", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveSSE(client, request, events, func(bus.Item) bool { return true }, func(bus.Item) (bool, error) { return true, nil })
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(client.body(), "event: message") {
		if time.Now().After(deadline) {
			t.Fatalf("an item published once the stream was open never reached it; the stream wrote %q", client.body())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
