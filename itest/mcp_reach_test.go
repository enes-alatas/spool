//go:build integration

package itest

import (
	"os"
	"path/filepath"
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
