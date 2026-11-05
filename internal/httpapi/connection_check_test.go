package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

const checkFixtureSecret = "fixture-check-secret-0000"

// mcpAnswer is an MCP server's answer to initialize.
const mcpAnswer = `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"fixture","version":"1"}}}`

func httpConnection(url, secret string) *store.Connection {
	return &store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Secret: secret,
		Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: url}}
}

// A check reads an MCP server's answer and the status around it, and says
// how it went in a coarse reason: never the server's body or headers,
// and never the secret, even from a server that echoes it back.
func TestCheckMCPServer(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	var deleted []string
	answer := func(status int, contentType, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if r.Method == http.MethodDelete {
				deleted = append(deleted, r.Header.Get("Mcp-Session-Id"))
				mu.Unlock()
				return
			}
			auth = append(auth, r.Header.Get("Authorization"))
			mu.Unlock()
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			w.Header().Set("X-Echo", r.Header.Get("Authorization"))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(strings.ReplaceAll(body, "ECHO", r.Header.Get("Authorization"))))
		}
	}
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the check followed a redirect to %s", r.URL)
	}))
	t.Cleanup(elsewhere.Close)
	cases := []struct {
		name       string
		handler    http.HandlerFunc
		secret     string
		sendSecret bool
		status     string
		reason     string
		sent       bool
	}{
		{"json answer, secret sent", answer(200, "application/json", mcpAnswer), checkFixtureSecret, true, checkOK, reasonOK, true},
		{"stream answer", answer(200, "text/event-stream", "event: message\ndata: "+mcpAnswer+"\n\n"), "", false, checkOK, reasonOK, false},
		{"answer without the secret", answer(200, "application/json", mcpAnswer), checkFixtureSecret, false, checkUnverified, reasonSecretUntested, false},
		{"secret refused", answer(401, "application/json", `{"error":"ECHO"}`), checkFixtureSecret, true, checkFailed, reasonSecretRefused, true},
		{"forbidden, secret withheld", answer(403, "", ""), checkFixtureSecret, false, checkUnverified, reasonSecretUntested, false},
		{"credential asked, none held", answer(401, "", ""), "", true, checkFailed, reasonCredentialMissing, false},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			auth = append(auth, r.Header.Get("Authorization"))
			mu.Unlock()
			http.Redirect(w, r, elsewhere.URL, http.StatusFound)
		}, checkFixtureSecret, true, checkFailed, reasonRedirected, true},
		{"a web page", answer(200, "text/html", "<html>ECHO</html>"), checkFixtureSecret, true, checkFailed, reasonNotMCP, true},
		{"json that isn't an initialize answer", answer(200, "application/json", `{"ok":"ECHO"}`), checkFixtureSecret, true, checkFailed, reasonNotMCP, true},
		{"server error", answer(502, "text/plain", "ECHO"), checkFixtureSecret, true, checkFailed, reasonHTTPError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			auth = nil
			mu.Unlock()
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			// a loopback url, so the guarded transport dials it
			view := checkMCPServer(context.Background(), httpConnection(server.URL+"/mcp", tc.secret), tc.sendSecret, nil)
			if view.Status != tc.status || view.Reason != tc.reason || view.SecretSent != tc.sent {
				t.Fatalf("check = %+v, want status %s reason %s secret_sent %v", view, tc.status, tc.reason, tc.sent)
			}
			raw, _ := json.Marshal(view)
			if strings.Contains(string(raw), checkFixtureSecret) {
				t.Fatalf("the check's answer carries the secret: %s", raw)
			}
			mu.Lock()
			defer mu.Unlock()
			wantAuth := ""
			if tc.sent {
				wantAuth = "Bearer " + checkFixtureSecret
			}
			if len(auth) != 1 || auth[0] != wantAuth {
				t.Fatalf("the server was sent Authorization %q, want one request with %q", auth, wantAuth)
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) == 0 || deleted[0] != "fixture-session" {
		t.Errorf("no check ended the session its server opened: %q", deleted)
	}
}

// A check sends nothing to plain http off this machine, and judges the
// address a name resolves to as it dials: link-local and metadata never,
// and this machine only for a url that names it.
func TestCheckMCPServerGuards(t *testing.T) {
	view := checkMCPServer(context.Background(), httpConnection("http://mcp.example.test/mcp", ""), false, nil)
	if view.Status != checkSkipped || view.Reason != reasonCleartext {
		t.Errorf("plain http off loopback: %+v, want skipped cleartext", view)
	}
	for _, url := range []string{"https://169.254.169.254/latest/meta-data/", "https://[fe80::1]/mcp", "https://[fd00:ec2::254]/mcp", "https://100.100.100.200/latest/meta-data/"} {
		view := checkMCPServer(context.Background(), httpConnection(url, checkFixtureSecret), true, nil)
		if view.Status != checkFailed || view.Reason != reasonAddressRefused {
			t.Errorf("%s: %+v, want failed address_refused", url, view)
		}
	}

	// a name that isn't loopback but reaches this machine: the transport
	// for an off-machine url refuses the loopback address it dials
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an off-machine check reached this machine")
	}))
	defer server.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	if _, err := guardedTransport(false).RoundTrip(req); err == nil || !strings.Contains(err.Error(), errAddressRefused.Error()) {
		t.Errorf("an off-machine transport dialled loopback: %v", err)
	}
	if reason, _ := dialFailure(&net.OpError{Op: "dial", Err: errAddressRefused}); reason != reasonAddressRefused {
		t.Errorf("a refused address reads as %s", reason)
	}

	for _, tc := range []struct {
		addr     string
		loopback bool
		want     bool
	}{
		{"127.0.0.1", true, true},
		{"::1", true, true},
		{"0.0.0.0", true, true},
		{"::", true, true},
		{"100.100.100.200", false, false},
		{"100.100.100.201", false, true},
		{"93.184.215.14", true, false},
		{"93.184.215.14", false, true},
		{"10.0.0.5", false, true},
		{"127.0.0.1", false, false},
		{"::ffff:127.0.0.1", false, false},
		{"0.0.0.0", false, false},
		{"169.254.169.254", false, false},
		{"::ffff:169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"fd00:ec2::254", false, false},
		{"224.0.0.1", false, false},
	} {
		if got := checkMayDial(netip.MustParseAddr(tc.addr), tc.loopback); got != tc.want {
			t.Errorf("checkMayDial(%s, loopback %v) = %v, want %v", tc.addr, tc.loopback, got, tc.want)
		}
	}
}

// A dial that fails says how, coarsely: refused, a TLS failure, a
// timeout.
func TestCheckMCPServerDialFailures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + listener.Addr().String() + "/mcp"
	_ = listener.Close()
	if view := checkMCPServer(context.Background(), httpConnection(closed, ""), false, nil); view.Reason != reasonUnreachable {
		t.Errorf("nothing listening: %+v, want unreachable", view)
	}

	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a check went past an untrusted certificate")
	}))
	defer tlsServer.Close()
	if view := checkMCPServer(context.Background(), httpConnection(tlsServer.URL+"/mcp", checkFixtureSecret), true, nil); view.Reason != reasonTLSFailed {
		t.Errorf("untrusted certificate: %+v, want tls_failed", view)
	}

	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer slow.Close()
	defer close(hang)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if view := checkMCPServer(ctx, httpConnection(slow.URL+"/mcp", ""), false, nil); view.Reason != reasonTimedOut {
		t.Errorf("no answer: %+v, want timed_out", view)
	}
}

// Each caller has its own cap on checks a minute, and the cap frees up as
// the minute passes.
func TestCheckLimiter(t *testing.T) {
	var limiter checkLimiter
	now := time.Now()
	for i := range checksPerMinute {
		if !limiter.allow("user:ada", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("check %d refused under the cap", i+1)
		}
	}
	if limiter.allow("user:ada", now.Add(30*time.Second)) {
		t.Error("a check past the cap was allowed")
	}
	if !limiter.allow("user:bo", now.Add(30*time.Second)) {
		t.Error("another caller was held to ada's cap")
	}
	if !limiter.allow("user:ada", now.Add(61*time.Second)) {
		t.Error("the cap didn't free up after a minute")
	}
}

// A check sends the secret only where a live loop holding the connection
// has the hub broker it: never for an archived one, a cloud one or one
// that doesn't hold it, and a loopback server only for a bare loop.
func TestCheckReach(t *testing.T) {
	remote := httpConnection("https://mcp.example.test/mcp", checkFixtureSecret)
	remote.LoopIDs = []string{"l1"}
	local := httpConnection("http://127.0.0.1:8931/mcp", checkFixtureSecret)
	local.LoopIDs = []string{"l1"}
	loop := func(runtime, status string) []*store.Loop {
		return []*store.Loop{{ID: "l1", Runtime: runtime, Status: status}, {ID: "l2", Runtime: store.RuntimeBare}}
	}
	for _, tc := range []struct {
		name            string
		connection      *store.Connection
		loops           []*store.Loop
		send, checkable bool
	}{
		{"remote, held by a docker loop", remote, loop(store.RuntimeDocker, ""), true, true},
		{"remote, held by a bare loop", remote, loop(store.RuntimeBare, ""), true, true},
		{"remote, held by an archived loop", remote, loop(store.RuntimeBare, store.StatusArchived), false, true},
		{"remote, held by a cloud loop", remote, loop(store.RuntimeCloud, ""), false, true},
		{"remote, held by no loop", remote, nil, false, true},
		{"loopback, held by a bare loop", local, loop(store.RuntimeBare, ""), true, true},
		{"loopback, held by an archived bare loop", local, loop(store.RuntimeBare, store.StatusArchived), false, false},
		{"loopback, held by a docker loop", local, loop(store.RuntimeDocker, ""), false, false},
	} {
		send, checkable := checkReach(tc.connection, tc.loops)
		if send != tc.send || checkable != tc.checkable {
			t.Errorf("%s: send %v checkable %v, want %v %v", tc.name, send, checkable, tc.send, tc.checkable)
		}
	}
}
