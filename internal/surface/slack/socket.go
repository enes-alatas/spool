package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Socket Mode (ADR-0034) is how a loop's app hears from Slack without a
// public URL: the hub asks apps.connections.open for a WebSocket URL, dials
// it, and Slack pushes every event down it as an envelope the hub must
// acknowledge. The protocol is ours; coder/websocket only does the framing.

// linkTiming is how a link paces itself. Tests shorten it; nothing else
// varies it.
type linkTiming struct {
	// backoffMin and backoffMax bound the wait before a link that failed
	// tries again. A connection that reached hello starts the ladder over:
	// it was working, and whatever ended it is news, not a pattern. A
	// disabled link waits backoffMax between checks.
	backoffMin, backoffMax time.Duration
	// pingInterval is how often a link checks that Slack still answers,
	// and pingTimeout how long it waits for the answer. A connection idle
	// for hours can die without either side closing it, and a dead one
	// reads nothing forever rather than failing.
	pingInterval, pingTimeout time.Duration
}

var defaultLinkTiming = linkTiming{
	backoffMin:   time.Second,
	backoffMax:   5 * time.Minute,
	pingInterval: 30 * time.Second,
	pingTimeout:  10 * time.Second,
}

const (
	// frameLimit is the largest frame a link reads. coder/websocket's
	// default of 32 KiB is smaller than a long message's event.
	frameLimit = 1 << 20
)

// disconnectLinkDisabled is the disconnect reason Slack gives when the app's
// Socket Mode was switched off. No reconnect can work until the operator
// turns it back on in Slack, which Spool is not told of, so the link checks
// at the longest backoff instead of stopping. The other reasons are asks to
// reconnect: refresh_requested (Slack recycles every connection after a few
// hours) and warning (this one closes in about ten seconds).
const disconnectLinkDisabled = "link_disabled"

// frame is one message Slack sends down a Socket Mode connection. An
// envelope (events_api, and the interactive kinds a loop's app does not
// subscribe to) carries an EnvelopeID and must be acknowledged, or Slack
// delivers it again.
type frame struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Reason     string          `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
}

// link is one loop's Socket Mode connection, and the goroutine that keeps
// it: connect, read, acknowledge, and connect again when Slack or the
// network ends it.
type link struct {
	loopID     string
	credential string // the app-level token the link connects with
	cancel     context.CancelFunc
	done       chan struct{}

	mu          sync.Mutex
	connected   bool
	lastEventAt int64 // unix ms of the last envelope, 0 = none yet
	lastError   string
	ignored     int // envelopes acknowledged and not ingested
}

// status is the link as the control room renders it (#230).
func (link *link) status() map[string]any {
	link.mu.Lock()
	defer link.mu.Unlock()
	return map[string]any{
		"connected":      link.connected,
		"last_event_at":  link.lastEventAt,
		"last_error":     link.lastError,
		"ignored_events": link.ignored,
	}
}

func (link *link) up() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.connected = true
	link.lastError = ""
}

func (link *link) down(reason string) {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.connected = false
	if reason != "" {
		link.lastError = reason
	}
}

// received counts an envelope. Nothing is ingested yet (#230's next
// slice), so every one is counted as ignored.
func (link *link) received() {
	link.mu.Lock()
	defer link.mu.Unlock()
	link.lastEventAt = time.Now().UnixMilli()
	link.ignored++
}

// linkDisabled is a disabled link's last_error. It names what brings the
// link back, and when.
const linkDisabled = "Slack disabled Socket Mode for this app. Turn it back on in the app's settings; " +
	"the link checks every few minutes and reconnects once it is on"

// run keeps the link connected until ctx ends.
func (adapter *Adapter) run(ctx context.Context, link *link) {
	defer close(link.done)
	timing := adapter.timing
	backoff := time.Duration(0)
	disabled := false
	for {
		if backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		reachedHello, reason, err := adapter.connect(ctx, link, timing)
		if ctx.Err() != nil {
			link.down("")
			return
		}
		if reachedHello {
			disabled = false
		}
		switch {
		case reason == disconnectLinkDisabled:
			if !disabled {
				adapter.log.Warn("slack: socket mode disabled", "loop", link.loopID)
			}
			disabled = true
			link.down(linkDisabled)
			backoff = timing.backoffMax
		case reason != "":
			// Slack is ending this connection and expects another
			link.down("")
			backoff = 0
		case disabled:
			// how a disabled app's next attempt fails is not the news:
			// that it is still disabled is
			backoff = timing.backoffMax
		default:
			link.down(err.Error())
			adapter.log.Warn("slack: socket mode connection lost", "loop", link.loopID, "err", err)
			if reachedHello {
				backoff = timing.backoffMin
			} else {
				backoff = min(max(backoff*2, timing.backoffMin), timing.backoffMax)
			}
		}
	}
}

// connect runs one Socket Mode connection to its end. It returns whether
// Slack said hello on it, and either the reason Slack gave for closing it
// or the error that ended it.
func (adapter *Adapter) connect(ctx context.Context, link *link, timing linkTiming) (reachedHello bool, reason string, err error) {
	socketURL, err := adapter.client.OpenConnection(ctx, link.credential)
	if err != nil {
		return false, "", err
	}
	conn, _, err := websocket.Dial(ctx, socketURL, nil)
	if err != nil {
		return false, "", fmt.Errorf("slack socket mode: %w", withoutURL(err))
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(frameLimit)

	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	go keepAlive(connCtx, conn, timing)

	for {
		_, data, err := conn.Read(connCtx)
		if err != nil {
			return reachedHello, "", fmt.Errorf("slack socket mode: %w", err)
		}
		var msg frame
		if err := json.Unmarshal(data, &msg); err != nil {
			adapter.log.Warn("slack: unreadable socket mode frame", "loop", link.loopID, "err", err)
			continue
		}
		if msg.EnvelopeID != "" {
			// Acknowledged before anything is done with it: an envelope
			// Slack delivers again is a loop woken twice for one message.
			ack, _ := json.Marshal(map[string]string{"envelope_id": msg.EnvelopeID})
			if err := conn.Write(connCtx, websocket.MessageText, ack); err != nil {
				return reachedHello, "", fmt.Errorf("slack socket mode: ack: %w", err)
			}
			link.received()
			continue
		}
		switch msg.Type {
		case "hello":
			reachedHello = true
			link.up()
		case "disconnect":
			if msg.Reason == "" {
				msg.Reason = "unspecified"
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return reachedHello, msg.Reason, nil
		}
	}
}

// keepAlive pings until ctx ends, and closes the connection the first time
// Slack fails to answer, which ends the read that is waiting on it.
func keepAlive(ctx context.Context, conn *websocket.Conn, timing linkTiming) {
	ticker := time.NewTicker(timing.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, timing.pingTimeout)
		err := conn.Ping(pingCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			_ = conn.CloseNow()
			return
		}
	}
}

// withoutURL drops the URL from a failed dial's error. A Socket Mode URL
// carries a ticket that opens the app's connection, and the error is
// logged and shown in the control room.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("dial: %w", urlErr.Err)
	}
	return err
}
