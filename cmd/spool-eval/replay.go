package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enes-alatas/spool/internal/eval"
	"github.com/enes-alatas/spool/internal/httpapi"
)

// replay has a model play each case's turn again, from the loop's system
// prompt and the case's input, and writes what it did as a case the
// graders read. Nothing it does leaves the run: the CLI gets no built-in
// tools, and its only tools are the stub's send_message and Bash, which
// record a call and answer it with a made-up ref or no output.
func replay(args []string) error {
	flags := flag.NewFlagSet("replay", flag.ExitOnError)
	prompts := flags.String("prompts", "eval/cases/prompts", "directory of each loop's system prompt, <loop>.txt, written by export")
	out := flags.String("out", "eval/replays", "directory the replayed cases are written to")
	model := flags.String("model", "haiku", "the model the CLI replays with")
	from := flags.Int("from", 0, "index of the first case to replay, in name order")
	count := flags.Int("count", 10, "how many cases to replay: one batch")
	maxCalls := flags.Int("max-calls", 8, "tool calls a replay may make before the stub refuses the rest")
	budget := flags.Float64("budget-usd", 0.25, "the most one case's replay may spend (--max-budget-usd)")
	claudeBin := flags.String("claude", "claude", "the claude CLI")
	_ = flags.Parse(args)
	if flags.NArg() != 1 {
		return errors.New("usage: spool-eval replay [flags] <cases dir>")
	}

	paths, err := filepath.Glob(filepath.Join(flags.Arg(0), "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	if *from >= len(paths) {
		return fmt.Errorf("no cases from %d: there are %d", *from, len(paths))
	}
	paths = paths[*from:min(*from+*count, len(paths))]
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}

	var spent float64
	for _, path := range paths {
		original, err := readCase(path)
		if err != nil {
			return err
		}
		system, err := os.ReadFile(filepath.Join(*prompts, original.Loop+".txt"))
		if err != nil {
			return fmt.Errorf("%s: the loop's prompt: %w", original.ID, err)
		}
		stream, err := replayOne(*claudeBin, self, *model, string(system), original, *maxCalls, *budget)
		replayed, cost, readErr := eval.Replayed(original, stream)
		spent += cost
		if err == nil {
			err = readErr
		}
		if err != nil {
			fmt.Printf("%s  $%.4f  not graded: %v\n", filepath.Base(path), cost, err)
			continue
		}
		if err := writeCase(*out, replayed); err != nil {
			return err
		}
		fmt.Printf("%s  $%.4f\n", filepath.Base(path), cost)
	}
	fmt.Printf("batch of %d from %d: $%.4f\n", len(paths), *from, spent)
	return nil
}

// replayOne runs the CLI once on one case, in an empty directory with no
// project settings and no session kept, and returns its stream-json.
func replayOne(claudeBin, self, model, system string, original eval.Case, maxCalls int, budget float64) ([]byte, error) {
	dir, err := os.MkdirTemp("", "spool-eval-replay-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"spool": map[string]any{"command": self, "args": []string{"stub", "send", strconv.Itoa(maxCalls), filepath.Join(dir, "calls")}},
		"shell": map[string]any{"command": self, "args": []string{"stub", "shell", strconv.Itoa(maxCalls), filepath.Join(dir, "calls")}},
	}})
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		return nil, err
	}
	command := exec.Command(claudeBin, "-p",
		"--model", model,
		"--output-format", "stream-json", "--verbose",
		"--append-system-prompt", system,
		"--tools", "",
		"--mcp-config", configPath, "--strict-mcp-config",
		"--allowedTools", "mcp__spool__send_message,"+eval.StubBashTool,
		"--setting-sources", "project",
		"--disable-slash-commands",
		"--no-session-persistence",
		"--max-budget-usd", strconv.FormatFloat(budget, 'f', -1, 64),
	)
	command.Dir = dir
	command.Stdin = strings.NewReader(strings.Join(original.Input, "\n\n"))
	command.Stderr = os.Stderr
	return command.Output()
}

// stub serves one of the replay's two MCP servers on stdio: "send" offers
// the hub's own send_message, schema and description, and "shell" a Bash
// that runs nothing. Both servers of a run count calls in one file, and
// refuse every call past the limit, so a replay that loops ends.
func stub(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: spool-eval stub send|shell <max calls> <count file>")
	}
	limit, err := strconv.Atoi(args[1])
	if err != nil {
		return err
	}
	counter := &callCounter{path: args[2], limit: limit}
	server := mcp.NewServer(&mcp.Implementation{Name: "spool-eval-stub"}, nil)
	switch args[0] {
	case "send":
		var nextRef atomic.Int64
		nextRef.Store(900000)
		mcp.AddTool(server, &mcp.Tool{Name: httpapi.SendMessageName, Description: httpapi.SendMessageDescription},
			func(ctx context.Context, call *mcp.CallToolRequest, in httpapi.SendMessageIn) (*mcp.CallToolResult, httpapi.SendMessageOut, error) {
				if refused := counter.take(); refused != nil {
					return refused, httpapi.SendMessageOut{}, nil
				}
				id := nextRef.Add(1)
				return nil, httpapi.SendMessageOut{MessageID: id, Ref: "ref:" + strconv.FormatInt(id, 10)}, nil
			})
	case "shell":
		type bashIn struct {
			Command     string `json:"command" jsonschema:"the command to run"`
			Description string `json:"description,omitempty" jsonschema:"what the command does, in a few words"`
		}
		mcp.AddTool(server, &mcp.Tool{Name: "Bash", Description: "Executes a bash command and returns its output."},
			func(ctx context.Context, call *mcp.CallToolRequest, in bashIn) (*mcp.CallToolResult, any, error) {
				if refused := counter.take(); refused != nil {
					return refused, nil, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "(no output)"}}}, nil, nil
			})
	default:
		return fmt.Errorf("no stub %q", args[0])
	}
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// callCounter counts a replay's tool calls in a file both stubs share; the
// CLI calls tools one at a time, so a read and a rewrite do not race.
type callCounter struct {
	path  string
	limit int
}

// take counts one call, and returns the refusal to answer it with once
// the limit is spent.
func (counter *callCounter) take() *mcp.CallToolResult {
	raw, _ := os.ReadFile(counter.path)
	used, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if used >= counter.limit {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "tool call limit reached for this turn; end it now"}}}
	}
	_ = os.WriteFile(counter.path, []byte(strconv.Itoa(used+1)), 0o600)
	return nil
}

func readCase(path string) (eval.Case, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return eval.Case{}, err
	}
	var turnCase eval.Case
	if err := json.Unmarshal(raw, &turnCase); err != nil {
		return eval.Case{}, fmt.Errorf("%s: %w", path, err)
	}
	return turnCase, nil
}
