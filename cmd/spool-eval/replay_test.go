package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enes-alatas/spool/internal/eval"
)

// TestMain lets the test binary stand in for spool-eval when a test runs it
// as a replay's stub, the way the CLI would.
func TestMain(tests *testing.M) {
	if args := os.Getenv("SPOOL_EVAL_AS_MAIN"); args != "" {
		os.Args = append([]string{"spool-eval"}, strings.Fields(args)...)
		main()
		os.Exit(0)
	}
	os.Exit(tests.Run())
}

// connectStub starts the test binary as one stub and connects to it.
func connectStub(t *testing.T, kind, countFile string, limit string) *mcp.ClientSession {
	t.Helper()
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), "SPOOL_EVAL_AS_MAIN=stub "+kind+" "+limit+" "+countFile)
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestTheStubsRecordNothingAndStopAtTheLimit(t *testing.T) {
	ctx := context.Background()
	countFile := filepath.Join(t.TempDir(), "calls")
	send := connectStub(t, "send", countFile, "2")
	shell := connectStub(t, "shell", countFile, "2")

	tools, err := send.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "send_message" {
		t.Fatalf("send stub tools = %+v, %v; want the hub's send_message alone", tools, err)
	}
	schema, _ := json.Marshal(tools.Tools[0].InputSchema)
	if !strings.Contains(string(schema), "owner_dm (your owner") {
		t.Errorf("send_message's schema is not the hub's: %s", schema)
	}

	sent, err := send.CallTool(ctx, &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"destination": "channel:fleet", "text": "hi"}})
	if err != nil || sent.IsError || !strings.Contains(text(sent), `"ref":"ref:900001"`) {
		t.Fatalf("send = %+v, %v; want a made-up ref", sent, err)
	}
	ran, err := shell.CallTool(ctx, &mcp.CallToolParams{Name: "Bash", Arguments: map[string]any{"command": "gh pr merge 1"}})
	if err != nil || ran.IsError || text(ran) != "(no output)" {
		t.Fatalf("bash = %+v, %v; want no output", ran, err)
	}
	// The two servers share one count: the third call is past the limit.
	refused, err := send.CallTool(ctx, &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{"destination": "channel:fleet", "text": "again"}})
	if err != nil || !refused.IsError {
		t.Errorf("third call = %+v, %v; want it refused", refused, err)
	}
}

func text(result *mcp.CallToolResult) string {
	var out []string
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			out = append(out, textContent.Text)
		}
	}
	return strings.Join(out, "")
}

// TestAReplayGivesTheCLINoToolOfItsOwn runs replayOne against a stand-in
// claude that records how it was started.
func TestAReplayGivesTheCLINoToolOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	argsFile, stdinFile := filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	fake := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdinFile + "\n" +
		`echo '{"type":"result","result":"ok\n[next-wake: 1h]","total_cost_usd":0.01}'` + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	original := eval.Case{Loop: "terra", Input: []string{"[tick one]", "[tick two]"}}
	stream, err := replayOne(fake, "/bin/spool-eval", "haiku", "SYSTEM PROMPT", original, 8, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stream), `"total_cost_usd":0.01`) {
		t.Errorf("stream = %s", stream)
	}
	raw, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	after := func(flag string) string {
		index := slices.Index(args, flag)
		if index < 0 || index+1 >= len(args) {
			t.Fatalf("no %s in %q", flag, args)
		}
		return args[index+1]
	}
	if got := after("--tools"); got != "" {
		t.Errorf("--tools %q, want none", got)
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Error("no --strict-mcp-config: the user's own MCP servers would load")
	}
	if got := after("--allowedTools"); got != "mcp__spool__send_message,"+eval.StubBashTool {
		t.Errorf("--allowedTools %q", got)
	}
	if after("--model") != "haiku" || after("--append-system-prompt") != "SYSTEM PROMPT" || after("--max-budget-usd") != "0.25" {
		t.Errorf("args = %q", args)
	}
	stdin, _ := os.ReadFile(stdinFile)
	if string(stdin) != "[tick one]\n\n[tick two]" {
		t.Errorf("stdin = %q, want the case's input", stdin)
	}
}
