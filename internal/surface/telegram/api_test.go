package telegram

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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

// stallingBotAPI answers every call with an empty success, except the
// first, which it holds without an answer until the caller gives up on it:
// a connection that died after the request was written, as far as the
// caller can tell. It records the connection each call arrived on.
type stallingBotAPI struct {
	mu     sync.Mutex
	conns  []string // each call's remote address, in arrival order
	protos []int    // each call's HTTP major version
	hold   time.Duration
}

func (api *stallingBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// read whole, so the server watches the connection and sees the caller
	// hang up on a held call
	_, _ = io.Copy(io.Discard, r.Body)
	api.mu.Lock()
	api.conns = append(api.conns, r.RemoteAddr)
	api.protos = append(api.protos, r.ProtoMajor)
	first := len(api.conns) == 1
	api.mu.Unlock()
	if first && api.hold == 0 {
		<-r.Context().Done()
		return
	}
	if strings.HasSuffix(r.URL.Path, "/getUpdates") {
		time.Sleep(api.hold)
		_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
		return
	}
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":42}}}`)
}

func (api *stallingBotAPI) calls() ([]string, []int) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return slices.Clone(api.conns), slices.Clone(api.protos)
}

// TestAnUnansweredCallIsRetriedOnAFreshConnection: a call Telegram never
// answers gives up after the answer timeout, not the long poll's 70s, and
// the next call does not go out on the connection that swallowed it. Each
// of the 164 timeouts in #559 waited the full 70s, and its retries were
// written into the same dead connection.
func TestAnUnansweredCallIsRetriedOnAFreshConnection(t *testing.T) {
	api := &stallingBotAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	client := newClient(srv.URL, "synthetic-token", 200*time.Millisecond)

	start := time.Now()
	if _, err := client.SendMessage(context.Background(), 42, "hello", 0); err == nil {
		t.Fatal("a call with no answer was expected to fail")
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the call waited %v for an answer, past its answer timeout", waited)
	}
	if _, err := client.SendMessage(context.Background(), 42, "hello", 0); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	conns, _ := api.calls()
	if len(conns) != 2 || conns[0] == conns[1] {
		t.Fatalf("the retry went out on the stalled call's connection: %v", conns)
	}
}

// TestTheLongPollOutwaitsTheAnswerTimeout: getUpdates is answered only
// when an update comes or its timeout runs out, so the answer timeout that
// gives up on a send must not cut it short.
func TestTheLongPollOutwaitsTheAnswerTimeout(t *testing.T) {
	api := &stallingBotAPI{hold: 600 * time.Millisecond}
	srv := httptest.NewServer(api)
	defer srv.Close()
	client := newClient(srv.URL, "synthetic-token", 200*time.Millisecond)

	if _, err := client.GetUpdates(context.Background(), 0, 1); err != nil {
		t.Fatalf("the long poll was cut short: %v", err)
	}
}

// TestEachBotDialsItsOwnConnections: one bot's dead connection is no other
// bot's. Over the shared default pool, the second client below would reuse
// the first one's idle connection.
func TestEachBotDialsItsOwnConnections(t *testing.T) {
	api := &stallingBotAPI{hold: time.Millisecond}
	srv := httptest.NewServer(api)
	defer srv.Close()

	for _, token := range []string{"synthetic-alpha", "synthetic-beta"} {
		if _, err := newClient(srv.URL, token, time.Second).SendMessage(context.Background(), 42, "hi", 0); err != nil {
			t.Fatal(err)
		}
	}
	conns, _ := api.calls()
	if len(conns) != 2 || conns[0] == conns[1] {
		t.Fatalf("two bots' calls shared a connection: %v", conns)
	}
}

// TestCallsSpeakHTTP1: a server offering HTTP/2, as api.telegram.org does,
// still gets HTTP/1.1, where a call that times out takes its connection
// with it instead of leaving it pooled for every other call to stall on.
func TestCallsSpeakHTTP1(t *testing.T) {
	api := &stallingBotAPI{hold: time.Millisecond}
	srv := httptest.NewUnstartedServer(api)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	client := newClient(srv.URL, "synthetic-token", time.Second)
	client.http.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig

	if _, err := client.SendMessage(context.Background(), 42, "hi", 0); err != nil {
		t.Fatal(err)
	}
	if _, protos := api.calls(); len(protos) != 1 || protos[0] != 1 {
		t.Fatalf("the call spoke HTTP/%v, want HTTP/1.1", protos)
	}
}
