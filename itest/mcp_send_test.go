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
		Endpoint:             s.mcpURL + "/mcp",
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
		{"reply to a reference that names nothing", map[string]any{"destination": "group", "text": "@aster hi", "reply_to": "ref:9999"}, "unknown_reply_to"},
		{"owner_dm without a surface", map[string]any{"destination": "owner_dm", "text": "hello owner"}, "no_such_destination"},
	}
	for _, c := range cases {
		wantSendError(t, callSend(t, sess, c.args), c.code)
	}
	// A tick reply of aster's own may legitimately be stored; none of the
	// refused payloads may be.
	for _, m := range s.activity() {
		for _, refused := range []string{"talking to nobody", "@stranger hi", "hello owner"} {
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
	// Every turn start reopens the budget, and this test counts sends
	// against one turn's worth. Let the creation tick finish first, then
	// pause so no further turn begins: on a slow machine that turn
	// otherwise starts mid-count and the eleventh send lands in a fresh
	// budget.
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })
	s.mustJSON("POST", "/api/loops/aster/pause", nil, nil)
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
		Endpoint:             s.mcpURL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{"not-a-token"}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("connect with a bad token: err = %v, want Unauthorized", err)
	}
}

// TestFakeclaudeSendDirective: a scripted loop turn sends through the hub's
// MCP endpoint mid-turn — two messages from one turn — over the --mcp-config
// the runner passes to every claude spawn; the turn's final text reports the
// outcomes.
func TestFakeclaudeSendDirective(t *testing.T) {
	ws := workspaceWithScript(t,
		`!send {"destination":"control_room","text":"first note"} !send {"destination":"control_room","text":"second note"}`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})

	s.message("aster", "go")
	tn := s.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "sent")
	})
	if tn.IsError || strings.Contains(tn.ResultText, "send error") {
		t.Fatalf("scripted sends failed: %s", dump(tn))
	}

	found := map[string]bool{}
	for _, m := range s.activity() {
		if m.Conversation == "control_room" && m.Author == "aster" {
			found[m.Text] = true
		}
	}
	if !found["first note"] || !found["second note"] {
		t.Fatalf("scripted sends not stored as control_room messages: %s", dump(s.activity()))
	}
}

// TestSendBudgetResetsPerTurn: a turn that spends the whole send cap does
// not starve the next turn — the runner reopens the budget at every turn
// start.
func TestSendBudgetResetsPerTurn(t *testing.T) {
	// line 1 absorbs the creation tick (fakeclaude scripts are per-turn);
	// line 2 spends the whole cap, line 3 sends once more.
	full := strings.Repeat(`!send {"destination":"control_room","text":"burst"} `, 10)
	ws := workspaceWithScript(t, "!ctx 0\n"+full+"\n"+`!send {"destination":"control_room","text":"after reset"}`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})
	s.waitTurn("aster", 20*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })

	s.message("aster", "one")
	first := s.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "sent")
	})
	if strings.Contains(first.ResultText, "send error") {
		t.Fatalf("a send inside the cap was refused: %s", dump(first))
	}

	s.message("aster", "two")
	second := s.waitTurn("aster", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "sent") && tn.ID != first.ID
	})
	if strings.Contains(second.ResultText, "send_limit") {
		t.Fatalf("budget did not reopen for the next turn: %s", dump(second))
	}
}

// TestRedeliveredTurnKnowsItsSends: a session lost mid-turn after a send —
// the redelivered batch's fresh session is told what was already sent, and
// the send is stored exactly once.
func TestRedeliveredTurnKnowsItsSends(t *testing.T) {
	// line 1 absorbs the creation tick; line 2 sends and then dies the way
	// a lost session does; the fresh session's turn 1 is line 1 again and
	// echoes the redelivered batch.
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"control_room","text":"pre-loss"} !lost`+"\n")
	s := startServer(t, t.TempDir())
	s.createLoop("aster", map[string]any{"workspace_path": ws})
	s.waitTurn("aster", 20*time.Second, func(tn turn) bool { return tn.Trigger == "tick" })

	s.message("aster", "risky business")
	redelivered := s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return !tn.IsError && strings.Contains(tn.ResultText, "risky business") &&
			strings.Contains(tn.ResultText, "already sent")
	})
	// the note identifies the send — destination and content, not a bare
	// count — so the retry can tell what is already out (ADR-0026)
	if !strings.Contains(redelivered.ResultText, `to control_room: "pre-loss"`) {
		t.Fatalf("fresh session not told what the lost attempt sent: %s", dump(redelivered))
	}
	n := 0
	for _, m := range s.activity() {
		if m.Text == "pre-loss" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("lost attempt's send stored %d times, want exactly 1", n)
	}
}

// The loop-facing listener carries the MCP endpoint and nothing else (#238):
// the operator's API is not routed onto it, and the MCP endpoint is no longer
// routed onto the API's. Workstations are allowlisted to this port, so
// anything reachable here is reachable by every loop in the fleet.
func TestMCPListenerServesOnlyMCP(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("mcponly", nil)

	for _, path := range []string{"/api/health", "/api/loops", "/api/settings", "/"} {
		resp, err := http.Get(s.mcpURL + path)
		if err != nil {
			t.Fatalf("GET %s on the mcp listener: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s on the mcp listener = %d, want 404 — the API must not be routed here", path, resp.StatusCode)
		}
	}

	// And the other way round: the endpoint a loop authenticates to is gone
	// from the operator's listener, so moving it is a move and not a copy.
	// The failure has to be a 404 and not merely some failure: this binary
	// has no embedded control room, and one that has it would answer /mcp
	// with index.html if the API mux did not refuse the path itself.
	resp, err := http.Get(s.baseURL + "/mcp")
	if err != nil {
		t.Fatalf("GET /mcp on the API listener: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /mcp on the API listener = %d, want 404 — not the UI's catch-all", resp.StatusCode)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "itest", Version: "0"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             s.baseURL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{hubMCPToken(t, s, "mcponly")}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err == nil {
		sess.Close()
		t.Fatal("the API listener must no longer serve /mcp")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		// A build with the control room embedded would answer this path with
		// index.html if the API mux did not refuse it; the failure would then
		// be a parse error, and this test would still have passed.
		t.Errorf("connect on the API listener: err = %v, want the 404 the API mux serves", err)
	}
}
