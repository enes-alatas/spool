package claude

import "testing"

func TestSpoolMCPFailure(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"failed, as the fleet's store recorded it (#476)", `{"type":"system","subtype":"init","session_id":"s","mcp_servers":[{"name":"spool","status":"failed","source":"dynamic"}]}`, "failed"},
		{"needing auth", `{"type":"system","subtype":"init","mcp_servers":[{"name":"spool","status":"needs-auth"}]}`, "needs-auth"},
		{"connected", `{"type":"system","subtype":"init","mcp_servers":[{"name":"spool","status":"connected"}]}`, ""},
		{"still connecting", `{"type":"system","subtype":"init","mcp_servers":[{"name":"spool","status":"pending"}]}`, ""},
		{"another server failing", `{"type":"system","subtype":"init","mcp_servers":[{"name":"other","status":"failed"},{"name":"spool","status":"connected"}]}`, ""},
		{"no MCP servers at all", `{"type":"system","subtype":"init","session_id":"s"}`, ""},
	}
	for _, testCase := range cases {
		ev := DecodeEvent([]byte(testCase.line))
		if ev.Init == nil {
			t.Fatalf("%s: no init decoded", testCase.name)
		}
		if got := ev.Init.SpoolMCPFailure(); got != testCase.want {
			t.Errorf("%s: SpoolMCPFailure = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// An init's tools count toward MCP only when a server supplied them, and
// toward a server only when they carry its prefix: a server whose name
// starts another's does not borrow its tools.
func TestMCPTools(t *testing.T) {
	ev := DecodeEvent([]byte(`{"type":"system","subtype":"init","tools":["Bash","Read","mcp__spool__send_message",` +
		`"mcp__spool_extra__lookup","mcp__spool_extra__fetch"]}`))
	if ev.Init == nil {
		t.Fatal("no init decoded")
	}
	for server, want := range map[string]int{"": 3, "spool": 1, "spool_extra": 2, "absent": 0} {
		if got := ev.Init.MCPTools(server); got != want {
			t.Errorf("MCPTools(%q) = %d, want %d", server, got, want)
		}
	}
}
