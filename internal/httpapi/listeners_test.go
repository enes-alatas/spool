package httpapi

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// The operator listener bounds what a caller can make the hub hold, a
// header's read and an idle connection above all, and has no write
// timeout, which would cut the control room's stream (#877).
func TestTheOperatorListenerBoundsWhatACallerHolds(t *testing.T) {
	srv := OperatorHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("the operator listener leaves a bound unset: header %s, read %s, idle %s, header bytes %d",
			srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout, srv.MaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("the operator listener has a write timeout of %s; a stream would be cut", srv.WriteTimeout)
	}
}

// Every /api body but an upload's is bounded before any credential is
// read, the sign-in's included: one declared too large is refused unread,
// and one that runs past the bound undeclared stops being read there.
func TestAPIBodiesAreBounded(t *testing.T) {
	server := &Server{OperatorToken: testToken, ListenAddr: "127.0.0.1:8080", Users: testUsers(t)}
	var read int64
	var readErr error
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", server.handleLogin)
	mux.HandleFunc("POST /api/loops", func(w http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	})
	handler := server.guard(mux)

	big := `{"username":"admin","password":"` + strings.Repeat("a", maxAPIBody) + `"}`
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(big))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a sign-in body over the bound = %d %s; want 413", rec.Code, rec.Body)
	}

	req = httptest.NewRequest("POST", "/api/loops", io.LimitReader(endless{}, 4*maxAPIBody))
	req.Host = "127.0.0.1:8080"
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	var tooLarge *http.MaxBytesError
	if read > maxAPIBody || !errors.As(readErr, &tooLarge) {
		t.Errorf("an undeclared body four times the bound read %d bytes, %v; want it stopped at %d", read, readErr, maxAPIBody)
	}
}

// endless is a body that never ends; the test caps it, so a lost bound
// fails rather than hangs.
type endless struct{}

func (endless) Read(buffer []byte) (int, error) {
	for at := range buffer {
		buffer[at] = 'a'
	}
	return len(buffer), nil
}
