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
