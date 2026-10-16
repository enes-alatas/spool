package httpapi

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The loop listener bounds what a loop can make the hub hold, a header's
// read and an idle connection above all, and has no write timeout, which
// would cut a long tool call from under it.
func TestTheLoopListenerBoundsWhatALoopHolds(t *testing.T) {
	srv := LoopHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("the loop listener leaves a bound unset: header %s, read %s, idle %s, header bytes %d",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout, srv.MaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("the loop listener has a write timeout of %s; a long call would be cut", srv.WriteTimeout)
	}
}

// The loop listener closes a kept-alive connection once it sits idle,
// rather than holding it until the loop closes it: a workstation's close
// may never reach the hub. The idle limit is shortened here; its real
// value is checked above.
func TestTheLoopListenerClosesAnIdleConnection(t *testing.T) {
	srv := LoopHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.IdleTimeout = 200 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "GET /mcp HTTP/1.1\r\nHost: hub\r\nConnection: keep-alive\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the request = %v, %v", resp, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("an idle connection read %v; want the hub to have closed it", err)
	}
}

// The read bound ends with the request's read: a handler that runs past
// it keeps its context, so a brokered call or an /mcp stream is never cut
// by it. net/http clears the read deadline once the request is read; this
// guards against that changing. The bound is shortened here.
func TestTheReadBoundLeavesAHandlerRunning(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		alive := make(chan bool, 1)
		srv := LoopHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
				alive <- false
			case <-time.After(600 * time.Millisecond):
				alive <- true
			}
		}))
		srv.ReadTimeout = 150 * time.Millisecond
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = srv.Serve(listener) }()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"jsonrpc":"2.0"}`)
		}
		req, err := http.NewRequest(method, "http://"+listener.Addr().String()+"/mcp", body)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		if !<-alive {
			t.Errorf("a %s handler's context ended with the read bound; a long call would be cut", method)
		}
		_ = srv.Close()
	}
}
