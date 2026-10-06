//go:build integration

package itest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// brokeredServer is an http MCP server with one tool, "ping", that records
// the Authorization header of every request it is sent.
type brokeredServer struct {
	port int
	mu   sync.Mutex
	auth []string
}

// startBrokeredServer serves on the given address: loopback for a bare
// loop's hub, every interface for one a docker test reaches at the host.
func startBrokeredServer(t *testing.T, addr string) *brokeredServer {
	t.Helper()
	recorded := &brokeredServer{}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "tracker", Version: "0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, struct{}{}, nil
		})
		return server
	}, nil)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.mu.Lock()
		recorded.auth = append(recorded.auth, r.Header.Get("Authorization"))
		recorded.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)
	recorded.port = listener.Addr().(*net.TCPAddr).Port
	return recorded
}

// seen is every Authorization header the server has been sent so far.
func (server *brokeredServer) seen() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.auth...)
}

// The hub brokers an attached http MCP server (#622), here a bare loop's
// loopback one, which shares the hub's machine: the loop calls its
// tools through the loop listener, the server is sent the connection's
// secret and nothing else, and the loop's mcp-config never holds that
// secret. A loop that kept the brokered URL and its token is refused at the
// hub once the connection is detached, and again once it is revoked, and
// the server is never reached.
func TestTheHubBrokersAnHTTPMCPServer(t *testing.T) {
	t.Parallel()
	tracker := startBrokeredServer(t, "127.0.0.1:0")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	const secret = "fixture-BROKERED-key-0000"
	s.mustJSON("POST", "/api/connections", map[string]any{
		"name": "tracker", "kind": "mcp-server", "secret": secret,
		"config": map[string]any{"transport": "http", "url": fmt.Sprintf("http://127.0.0.1:%d/mcp", tracker.port)},
	}, nil)
	s.mustJSON("PUT", "/api/loops/aster/connections/tracker", nil, nil)
	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)

	// ask runs one directive as a message's turn and returns that turn,
	// passing over every turn an earlier ask already read.
	asked := map[string]bool{}
	ask := func(directive, want string) turn {
		t.Helper()
		s.scriptLoop("aster", directive)
		s.message("aster", "go")
		tn := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
			return !asked[tn.ID] && tn.Trigger == "message" && strings.HasPrefix(tn.ResultText, want)
		})
		asked[tn.ID] = true
		return tn
	}

	if tn := ask("!mcp-config-contains "+secret, "mcp-config contains:"); tn.ResultText != "mcp-config contains: no" {
		t.Fatalf("the loop's mcp-config holds the server's secret: %s", tn.ResultText)
	}
	if tn := ask("!mcp tracker ping", "mcp tracker:"); tn.ResultText != "mcp tracker: pong" {
		t.Fatalf("a brokered call = %q, want the server's answer", tn.ResultText)
	}
	seen := tracker.seen()
	if len(seen) == 0 {
		t.Fatal("the server was never reached")
	}
	for _, auth := range seen {
		if auth != "Bearer "+secret {
			t.Fatalf("the server was sent Authorization %q, want the connection's secret and nothing else", auth)
		}
	}

	if tn := ask("!mcp-save tracker", "mcp-save tracker:"); tn.ResultText != "mcp-save tracker: saved" {
		t.Fatalf("saving the brokered entry = %q", tn.ResultText)
	}
	refused := func(when string) {
		t.Helper()
		before := len(tracker.seen())
		tn := ask("!mcp-saved tracker ping", "mcp tracker:")
		if !strings.Contains(tn.ResultText, "error") {
			t.Fatalf("%s, a kept URL and token still reached the server: %s", when, tn.ResultText)
		}
		if after := len(tracker.seen()); after != before {
			t.Fatalf("%s, the hub forwarded %d requests to the server", when, after-before)
		}
	}
	s.mustJSON("DELETE", "/api/loops/aster/connections/tracker", nil, nil)
	refused("after a detach")
	s.mustJSON("PUT", "/api/loops/aster/connections/tracker", nil, nil)
	s.mustJSON("POST", "/api/connections/tracker/revoke", nil, nil)
	refused("after a revoke")
}

// The broker is the loop listener's alone: the operator's API answers its
// path 404 without asking who is calling, as it does /mcp (#238).
func TestTheAPIListenerDoesNotBroker(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	resp, err := http.Get(s.baseURL + "/mcp/connections/tracker")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /mcp/connections/tracker on the API = %d, want 404", resp.StatusCode)
	}
}
