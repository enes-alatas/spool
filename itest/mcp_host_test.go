//go:build integration

package itest

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hostTransport sends every request under another Host, the name a
// workstation was given, whatever address it dials.
type hostTransport struct{ host, token string }

func (h hostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Host = h.host
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Docker Desktop delivers a workstation's request to the host's loopback,
// still naming host.docker.internal. The MCP SDK refused that as DNS
// rebinding, so on a Mac no docker loop ever reached the hub (#508). The
// bearer token is what keeps a rebinding page out, and it still does.
func TestTheLoopListenerAnswersAWorkstationNamingHostDockerInternal(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("desk", nil)
	host := net.JoinHostPort("host.docker.internal", s.port(s.mcpURL))

	client := mcp.NewClient(&mcp.Implementation{Name: "itest", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             s.mcpURL + "/mcp",
		HTTPClient:           &http.Client{Transport: hostTransport{host: host, token: hubMCPToken(t, s, "desk")}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connecting over loopback as %s: %v", host, err)
	}
	t.Cleanup(func() { sess.Close() })
	if res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "through Docker Desktop"}); res.IsError {
		t.Fatalf("send_message as %s: %s", host, resultText(res))
	}

	req, err := http.NewRequest("POST", s.mcpURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Transport: hostTransport{host: "rebound.example:80"}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request naming another host with no token = %d, want 401", resp.StatusCode)
	}
}
