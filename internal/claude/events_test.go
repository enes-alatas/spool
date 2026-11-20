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

// TestBackgroundEvents: the level of running background commands, and the
// replay of the hub's own message, are read off the lines the CLI writes
// for them (#1709); a replay of anything else, and every other event, say
// neither.
func TestBackgroundEvents(t *testing.T) {
	two := DecodeEvent([]byte(`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1","task_type":"local_bash"},{"task_id":"b2"}]}`))
	if two.BackgroundTasks == nil || *two.BackgroundTasks != 2 {
		t.Fatalf("two running commands read as %v", two.BackgroundTasks)
	}
	none := DecodeEvent([]byte(`{"type":"system","subtype":"background_tasks_changed","tasks":[]}`))
	if none.BackgroundTasks == nil || *none.BackgroundTasks != 0 {
		t.Fatalf("an empty level read as %v", none.BackgroundTasks)
	}
	if init := DecodeEvent([]byte(`{"type":"system","subtype":"init","session_id":"s"}`)); init.BackgroundTasks != nil {
		t.Fatalf("init read as a level of %d", *init.BackgroundTasks)
	}
	replay := DecodeEvent([]byte(`{"type":"user","isReplay":true,"uuid":"u-1","message":{"role":"user","content":"hi"}}`))
	if replay.ReplayUUID != "u-1" {
		t.Fatalf("the replay's uuid read as %q", replay.ReplayUUID)
	}
	toolResult := DecodeEvent([]byte(`{"type":"user","uuid":"u-2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t"}]}}`))
	if toolResult.ReplayUUID != "" {
		t.Fatalf("a tool result read as the replay of %q", toolResult.ReplayUUID)
	}
}
