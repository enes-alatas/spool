//go:build integration

package itest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

// The send_message scenarios drive the hub's MCP endpoint as a plain MCP
// client — the runner doesn't hand loops the endpoint yet, so the tool's
// contract (ADR-0026) is exercised directly.

// hubMCPToken reads a loop's bearer token straight from spool.db: it is
// deliberately absent from every API response.
func hubMCPToken(t *testing.T, s *server, name string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dataDir, "spool.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var token string
	if err := db.QueryRow(`SELECT hub_mcp_token FROM loops WHERE name=?`, name).Scan(&token); err != nil {
		t.Fatalf("token for %s: %v", name, err)
	}
	return token
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpSession(t *testing.T, s *server, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "itest", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             s.baseURL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func callSend(t *testing.T, sess *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "send_message", Arguments: args,
	})
	if err != nil {
		t.Fatalf("send_message call: %v", err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// wantSendError asserts a refusal carrying the given typed code.
func wantSendError(t *testing.T, res *mcp.CallToolResult, code string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("send accepted, want %s refusal: %s", code, resultText(res))
	}
	if !strings.Contains(resultText(res), code) {
		t.Fatalf("refusal lacks code %s: %s", code, resultText(res))
	}
}

// TestMCPSendGroupDeliversToMentionedLoop: a group send @mentioning a peer is
// stored as a group message and delivered to that peer only.
func TestMCPSendGroupDeliversToMentionedLoop(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	res := callSend(t, sess, map[string]any{"destination": "group", "text": "@briar ping from aster"})
	if res.IsError {
		t.Fatalf("group send refused: %s", resultText(res))
	}

	tn := s.waitTurn("briar", 20*time.Second, func(tn turn) bool {
		return tn.Trigger == "message" && strings.Contains(tn.ResultText, "ping from aster")
	})
	if tn.IsError {
		t.Errorf("briar's turn errored: %s", dump(tn))
	}
	for _, m := range s.activity() {
		// briar's echoed reply also contains the text; the send itself is
		// the message aster authored.
		if m.Author == "aster" && strings.Contains(m.Text, "ping from aster") {
			if m.Conversation != "group" || m.Origin != "loop" {
				t.Errorf("stored send misclassified: %s", dump(m))
			}
			return
		}
	}
	t.Error("sent message not in activity")
}

// TestMCPSendRefusals: the typed errors of the send contract, each
// correctable in-turn — and none of them stores or delivers anything.
func TestMCPSendRefusals(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	cases := []struct {
		name string
		args map[string]any
		code string
	}{
		{"unknown destination", map[string]any{"destination": "shoutbox", "text": "hi"}, "invalid_destination"},
		{"empty text", map[string]any{"destination": "group", "text": "   "}, "empty_text"},
		{"group without recipients", map[string]any{"destination": "group", "text": "talking to nobody"}, "no_recipients"},
		{"group mentioning only unknowns", map[string]any{"destination": "group", "text": "@stranger hi"}, "no_recipients"},
		{"broadcast", map[string]any{"destination": "group", "text": "@all hello"}, "unsupported_broadcast"},
		{"reply reference", map[string]any{"destination": "group", "text": "@aster hi", "reply_to": "msg:1"}, "unsupported_reply_to"},
		{"owner DM never captured", map[string]any{"destination": "owner_dm", "text": "hello owner"}, "owner_dm_unavailable"},
	}
	for _, c := range cases {
		wantSendError(t, callSend(t, sess, c.args), c.code)
	}
	// A tick reply of aster's own may legitimately be stored; none of the
	// refused payloads may be.
	for _, m := range s.activity() {
		for _, refused := range []string{"talking to nobody", "@stranger hi", "@all hello", "hello owner"} {
			if strings.Contains(m.Text, refused) && !strings.Contains(m.Text, "echo:") {
				t.Errorf("a refused send was stored: %s", dump(m))
			}
		}
	}
}

// TestMCPSendControlRoom: a control_room send is the loop's private web
// thread — stored against the loop, delivered to no loop.
func TestMCPSendControlRoom(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	res := callSend(t, sess, map[string]any{"destination": "control_room", "text": "status for the operator"})
	if res.IsError {
		t.Fatalf("control_room send refused: %s", resultText(res))
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "status for the operator") {
			if m.Conversation != "control_room" || m.ConversationLoopID == "" || len(m.DeliveredTo) != 0 {
				t.Errorf("control_room send misclassified: %s", dump(m))
			}
			return
		}
	}
	t.Fatal("control_room send not in activity")
}

// TestMCPSendCap: the per-turn budget refuses the send after the cap, with
// the typed code the model can act on.
func TestMCPSendCap(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	sess := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	for i := 0; i < 10; i++ {
		res := callSend(t, sess, map[string]any{"destination": "control_room", "text": fmt.Sprintf("note %d", i)})
		if res.IsError {
			t.Fatalf("send %d refused before the cap: %s", i+1, resultText(res))
		}
	}
	wantSendError(t, callSend(t, sess, map[string]any{"destination": "control_room", "text": "one too many"}), "send_limit")
}

// TestMCPBadToken: an unknown bearer never reaches the tool.
func TestMCPBadToken(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "itest", Version: "0"}, nil)
	_, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             s.baseURL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{"not-a-token"}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("connect with a bad token: err = %v, want Unauthorized", err)
	}
}
