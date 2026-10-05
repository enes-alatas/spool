//go:build integration

package itest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type mcpReachView struct {
	CurrentSessionID string `json:"current_session_id"`
	MCPSessionID     string `json:"mcp_session_id"`
	MCPServers       []struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		ToolCount int    `json:"tool_count"`
	} `json:"mcp_servers"`
	ToolCount *int `json:"tool_count"`
}

// The loop view says what a loop can reach through MCP, as its session's
// init reported it: the hub's server and its one tool, never the CLI's
// built-ins. It is absent after a restart until the loop's session starts
// again, and a session whose server failed says so, with no tools (#489).
func TestTheLoopViewShowsWhatMCPReaches(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	s := startServer(t, dataDir)
	s.createLoop("aster", nil)
	s.message("aster", "hello")
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "hello") })

	var reach mcpReachView
	s.mustJSON("GET", "/api/loops/aster", nil, &reach)
	if len(reach.MCPServers) != 1 || reach.MCPServers[0].Name != "spool" || reach.MCPServers[0].Status != "connected" ||
		reach.MCPServers[0].ToolCount != 1 || reach.ToolCount == nil || *reach.ToolCount != 1 {
		t.Fatalf("the loop view reports %+v, want the spool server connected with one tool", reach)
	}
	if reach.MCPSessionID == "" || reach.MCPSessionID != reach.CurrentSessionID {
		t.Fatalf("mcp_session_id = %q, want the current session %q", reach.MCPSessionID, reach.CurrentSessionID)
	}
	s.stop()

	s = startServer(t, dataDir)
	_, body := s.do("GET", "/api/loops/aster", nil)
	for _, field := range []string{"mcp_servers", "tool_count", "mcp_session_id"} {
		if strings.Contains(string(body), `"`+field+`"`) {
			t.Fatalf("after a restart and before any init, the loop view carries %s: %s", field, body)
		}
	}

	if err := os.MkdirAll(s.fkState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.fkState, "mcp-failed"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.message("aster", "are you there")
	s.waitState("aster", "workstation_down", 30*time.Second)
	reach = mcpReachView{}
	s.mustJSON("GET", "/api/loops/aster", nil, &reach)
	if len(reach.MCPServers) != 1 || reach.MCPServers[0].Status != "failed" || reach.MCPServers[0].ToolCount != 0 ||
		reach.ToolCount == nil || *reach.ToolCount != 0 {
		t.Fatalf("after a failed connect the loop view reports %+v, want spool failed with no tools", reach)
	}
}

// An attached mcp-server connection is one more server the loop's claude is
// given at its next session, by either transport, and detaching it takes it
// away again. The hub's own server stays throughout. No API response carries
// a server's secret, and claude's own env doesn't either (ADR-0043).
func TestAnAttachedMCPServerReachesTheLoop(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	servers := func(text string) []string {
		t.Helper()
		s.message("aster", text)
		s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, text) })
		var reach mcpReachView
		s.mustJSON("GET", "/api/loops/aster", nil, &reach)
		var names []string
		for _, server := range reach.MCPServers {
			names = append(names, server.Name+":"+server.Status)
		}
		return names
	}
	if got, want := servers("hello"), []string{"spool:connected"}; !slices.Equal(got, want) {
		t.Fatalf("with nothing attached, init reported %v, want %v", got, want)
	}
	const trackerKey, handbookKey = "fixture-TRACKER-key-0000", "fixture-HANDBOOK-key-0000"

	for _, connection := range []map[string]any{
		{"name": "tracker", "kind": "mcp-server", "secret": trackerKey,
			"config": map[string]any{"transport": "http", "url": "https://mcp.example.test/"}},
		{"name": "handbook", "kind": "mcp-server", "secret": handbookKey,
			"config": map[string]any{"transport": "stdio", "command": "handbook-mcp", "args": []string{"--read-only"}, "env": "HANDBOOK_KEY"}},
	} {
		s.mustJSON("POST", "/api/connections", connection, nil)
		s.mustJSON("PUT", "/api/loops/aster/connections/"+connection["name"].(string), nil, nil)
	}
	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)
	if got, want := servers("attached"), []string{"spool:connected", "handbook:connected", "tracker:connected"}; !slices.Equal(got, want) {
		t.Fatalf("with two servers attached, init reported %v, want %v", got, want)
	}
	for _, path := range []string{"/api/loops/aster", "/api/loops", "/api/connections"} {
		if _, body := s.do("GET", path, nil); strings.Contains(string(body), trackerKey) || strings.Contains(string(body), handbookKey) {
			t.Fatalf("GET %s carries an mcp-server's secret: %s", path, body)
		}
	}

	s.mustJSON("DELETE", "/api/loops/aster/connections/tracker", nil, nil)
	s.mustJSON("POST", "/api/loops/aster/rotate", nil, nil)
	if got, want := servers("after the handover"), []string{"spool:connected", "handbook:connected"}; !slices.Equal(got, want) {
		t.Fatalf("a session after tracker was detached reported %v, want %v", got, want)
	}

	// The stdio server's secret is in the config it is started from, never
	// in claude's own env. Exactly empty: a leak would come back redacted,
	// and a placeholder would pass a check for the value's absence.
	s.scriptLoop("aster", "!env HANDBOOK_KEY")
	s.message("aster", "what is in your env")
	tn := s.waitTurn("aster", 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, "HANDBOOK_KEY=") })
	if tn.ResultText != "HANDBOOK_KEY=" {
		t.Fatalf("claude's env carries the stdio server's secret: %q, want HANDBOOK_KEY= and nothing after it", tn.ResultText)
	}
}
