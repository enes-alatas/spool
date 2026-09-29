package slack

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// socketPeer is a Slack stand-in for one app's link: apps.connections.open
// hands out a URL on itself, and each connection behaves as mode says.
type socketPeer struct {
	srv  *httptest.Server
	stop chan struct{}

	mu    sync.Mutex
	mode  string // "normal", "disabled" or "deaf"
	opens int
}

func startSocketPeer(t *testing.T, mode string) *socketPeer {
	t.Helper()
	peer := &socketPeer{mode: mode, stop: make(chan struct{})}
	peer.srv = httptest.NewServer(http.HandlerFunc(peer.handle))
	// Cleanups run last-in first-out: the deaf handlers are released
	// before the server waits for them to return.
	t.Cleanup(peer.srv.Close)
	t.Cleanup(func() { close(peer.stop) })
	return peer
}

func (peer *socketPeer) setMode(mode string) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	peer.mode = mode
}

func (peer *socketPeer) opened() int {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.opens
}

func (peer *socketPeer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/apps.connections.open" {
		peer.mu.Lock()
		peer.opens++
		peer.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": "ws" + strings.TrimPrefix(peer.srv.URL, "http") + "/link"})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	peer.mu.Lock()
	mode := peer.mode
	peer.mu.Unlock()
	if mode == "disabled" {
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"disconnect","reason":"link_disabled"}`))
		return
	}
	if conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"hello","num_connections":1}`)) != nil {
		return
	}
	if mode == "deaf" {
		// A coder/websocket peer answers pings only while it reads, so one
		// that never reads again is, to the client, a network that died
		// without closing anything.
		select {
		case <-peer.stop:
		case <-r.Context().Done():
		}
		return
	}
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}

// runLink runs one link against peer on fast timing, until the test ends.
func runLink(t *testing.T, peer *socketPeer, timing linkTiming) *link {
	t.Helper()
	adapter := &Adapter{client: NewClientAt(peer.srv.URL), log: slog.New(slog.NewTextHandler(io.Discard, nil)), timing: timing}
	ctx, cancel := context.WithCancel(context.Background())
	running := &link{loopID: "l1", credential: "xapp-synthetic-fixture", cancel: cancel, done: make(chan struct{})}
	go adapter.run(ctx, running)
	t.Cleanup(func() { cancel(); <-running.done })
	return running
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func connected(running *link) bool {
	running.mu.Lock()
	defer running.mu.Unlock()
	return running.connected
}

func lastError(running *link) string {
	running.mu.Lock()
	defer running.mu.Unlock()
	return running.lastError
}

// Slack tells Spool when an app's Socket Mode is switched off, and nothing
// when it is switched back on. So a disabled link keeps checking, slowly,
// and turning Socket Mode back on is all the operator has to do.
func TestDisabledLinkComesBackWhenReenabled(t *testing.T) {
	peer := startSocketPeer(t, "disabled")
	running := runLink(t, peer, linkTiming{backoffMin: time.Millisecond, backoffMax: 50 * time.Millisecond,
		pingInterval: time.Minute, pingTimeout: time.Minute})

	waitFor(t, "the link reports Socket Mode disabled", func() bool {
		return strings.Contains(lastError(running), "disabled Socket Mode")
	})
	waitFor(t, "a disabled link checks again", func() bool { return peer.opened() >= 3 })
	if connected(running) || !strings.Contains(lastError(running), "disabled Socket Mode") {
		t.Fatalf("while disabled: connected %v, last_error %q", connected(running), lastError(running))
	}

	peer.setMode("normal")
	waitFor(t, "the link reconnects once Socket Mode is back on", func() bool { return connected(running) })
	if got := lastError(running); got != "" {
		t.Fatalf("a reconnected link still reports %q", got)
	}
}

// A connection whose network died sends nothing and closes nothing: the
// read waiting on it would wait forever. The ping is what notices, and the
// link dials a fresh URL.
func TestUnansweredPingDropsTheLink(t *testing.T) {
	peer := startSocketPeer(t, "deaf")
	running := runLink(t, peer, linkTiming{backoffMin: time.Millisecond, backoffMax: 50 * time.Millisecond,
		pingInterval: 20 * time.Millisecond, pingTimeout: 20 * time.Millisecond})

	waitFor(t, "the link connects", func() bool { return connected(running) })
	waitFor(t, "a link whose pings go unanswered dials again", func() bool { return peer.opened() >= 2 })
}
