package telegram

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTransportErrorsCarryNoToken: the bot token is in every request URL, and
// net/http puts the URL in transport errors. Since #147 those errors are
// stored on the message and served by the API, so a leak here is a
// credential in an API response, not just in a log.
func TestTransportErrorsCarryNoToken(t *testing.T) {
	// shaped like a bot token, and deliberately not one: a test fixture that
	// is a real credential is the bug this file exists to prevent
	const token = "0000000000:AA-not-a-real-bot-token-0000000000000"
	// a port nothing is listening on, so Do fails with the URL in its message
	client := NewClientAt("http://127.0.0.1:1", token)
	err := client.call(context.Background(), "sendMessage", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the bot token is in the error text: %v", err)
	}
	if !strings.Contains(err.Error(), "<bot-token>") {
		t.Fatalf("the error lost the shape of what was redacted: %v", err)
	}
}

// TestSendFailureLogsNoToken drives the bridge's own sender, because a clean
// Error() is only half the promise: the token must also be absent from what
// the bridge writes when a send fails — the log line #146 names.
func TestSendFailureLogsNoToken(t *testing.T) {
	// shaped like a bot token, and deliberately not one: a test fixture that
	// is a real credential is the bug this file exists to prevent
	const token = "0000000000:AA-not-a-real-bot-token-0000000000000"
	var logged safeBuffer
	br := &Bridge{log: slog.New(slog.NewTextHandler(&logged, nil))}
	bot := &poller{
		loopID: "l1", name: "alpha",
		client: NewClientAt("http://127.0.0.1:1", token),
		sendCh: make(chan sendReq, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go br.sendLoop(ctx, bot)
	bot.sendCh <- sendReq{chatID: 42, text: "hello"}

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logged.String(), "telegram send failed") {
		if time.Now().After(deadline) {
			t.Fatalf("the failed send was never logged: %s", logged.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logged.String(), token) {
		t.Fatalf("the bot token is in the log line: %s", logged.String())
	}
}

// safeBuffer is a bytes.Buffer a test can read while the sender writes to it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *safeBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(data)
}

func (buffer *safeBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
