// fakeclaude is a stand-in for the claude CLI used by tier-2 integration
// tests (docs/adr/0009). It speaks the exact observed stream-json protocol of
// the version pinned in internal/claude/preflight.go: silence until the first
// stdin user message, then system/init followed by assistant + result events
// per turn; --resume of an unknown session exits 1 with the canonical stderr
// line; stdin EOF exits 0.
//
// Session state (a file per session id) lives in $FAKECLAUDE_STATE so resume
// semantics survive process death, like the real CLI's session files.
//
// Replies: if the working directory contains a ".fakeclaude" file, its lines
// script the replies (line N answers the session's turn N; the last line
// repeats). Directives: "!crash" exits 2 mid-turn without a result; "!lost"
// dies mid-turn the way a lost session does (exit 1, canonical stderr);
// "!huge <bytes>" replies with that many bytes; "!hang <seconds>" sleeps
// first. A
// "!toolong" returns an errored result saying the prompt did not fit the
// window, with no usage, and keeps the session. A
// "!ctx <tokens>" prefix makes the turn report that many input tokens per API
// step — how a filling context looks from outside — and composes with the
// rest of the line ("!ctx 120000 !hang 2"). A "!steps <k>" prefix makes the
// turn span k API steps, the way a tool-using turn does: one assistant event
// per step, each with that step's own usage, while the result event reports
// the turn's summed usage — so a runner that reads the result's sum as
// context occupancy sees k times the real fill. "!sysprompt" replies with the text spool
// passed as --append-system-prompt, so a test can see the prompt a loop was
// given. Without a script file, every turn echoes: "echo: <received text>".
//
// A "!send {json}" prefix calls the hub's send_message MCP tool with the
// given arguments, exactly as the real CLI would mid-turn. It repeats for
// several sends and composes with a trailing reply text; with none, the
// reply reports each call's outcome ("sent …" / "send error: …"). The
// endpoint comes from --mcp-config (inline JSON or a file path, the real
// CLI's flag) or, when the runner doesn't pass one, from the
// FAKECLAUDE_MCP_CONFIG env var in the same format — read lazily at send
// time, so a test can write the file after the loop exists.
//
// A ".fakeclaude-resume-broken" file in the working directory makes every
// --resume fail the way a session that can no longer be loaded does: a
// diagnostic on stderr, exit 1, no stream-json. Fresh sessions still work.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sessionState struct {
	Turns int `json:"turns"`
}

func main() {
	var sessionID, resumeID, model, systemPrompt, mcpConfig string
	partials := false

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--version":
			fmt.Println("2.1.233 (fakeclaude)")
			return
		case "--session-id":
			i++
			sessionID = args[i]
		case "--resume":
			i++
			resumeID = args[i]
		case "--include-partial-messages":
			partials = true
		case "--model":
			i++
			model = args[i] // echoed back at init, as the real CLI resolves and reports it
		case "--append-system-prompt":
			i++
			systemPrompt = args[i] // replayed by the !sysprompt directive
		case "--mcp-config":
			i++
			mcpConfig = args[i] // inline JSON or a file path, like the real CLI
		case "--input-format", "--output-format", "--permission-mode",
			"--effort", "--add-dir":
			i++ // value consumed, ignored
		default:
			// -p, --verbose, unknown flags: ignored
		}
	}

	stateDir := os.Getenv("FAKECLAUDE_STATE")
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "fakeclaude-state")
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: state dir: %v\n", err)
		os.Exit(1)
	}

	// A session the CLI can no longer load — what an over-full context looks
	// like from the outside: a resume that dies with a diagnostic on stderr
	// and no stream-json at all. A fresh session still works, so a runner
	// that rotates recovers and one that retries does not.
	if resumeID != "" {
		if _, err := os.Stat(".fakeclaude-resume-broken"); err == nil {
			fmt.Fprintln(os.Stderr, "API Error: 400 prompt is too long: 251000 tokens > 200000 maximum")
			os.Exit(1)
		}
	}

	id := sessionID
	state := sessionState{}
	if resumeID != "" {
		id = resumeID
		data, err := os.ReadFile(filepath.Join(stateDir, resumeID+".json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "No conversation found with session ID: %s\n", resumeID)
			os.Exit(1)
		}
		_ = json.Unmarshal(data, &state)
	}

	script := loadScript()
	cwd, _ := os.Getwd()

	out := bufio.NewWriter(os.Stdout)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 10*1024*1024)

	first := true
	for in.Scan() {
		var msg struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil {
			continue
		}
		text := ""
		for _, c := range msg.Message.Content {
			text += c.Text
		}

		if first {
			first = false
			emit(map[string]any{
				"type": "system", "subtype": "init",
				"session_id": id, "cwd": cwd, "model": initModel(model),
			})
		}

		state.Turns++
		reply := "echo: " + text
		ctxTokens := 0
		steps := 1
		if script != nil {
			line := script[min(state.Turns, len(script))-1]
			for {
				if strings.HasPrefix(line, "!ctx ") {
					numStr, rest, _ := strings.Cut(strings.TrimPrefix(line, "!ctx "), " ")
					ctxTokens, _ = strconv.Atoi(numStr)
					line = strings.TrimSpace(rest)
					continue
				}
				if strings.HasPrefix(line, "!steps ") {
					numStr, rest, _ := strings.Cut(strings.TrimPrefix(line, "!steps "), " ")
					if n, err := strconv.Atoi(numStr); err == nil && n > 0 {
						steps = n
					}
					line = strings.TrimSpace(rest)
					continue
				}
				break
			}
			var sent []string
			for strings.HasPrefix(line, "!send ") {
				rest := strings.TrimPrefix(line, "!send ")
				dec := json.NewDecoder(strings.NewReader(rest))
				var sendArgs map[string]any
				if err := dec.Decode(&sendArgs); err != nil {
					sent = append(sent, "send error: bad json: "+err.Error())
					line = ""
					break
				}
				sent = append(sent, mcpSend(mcpConfig, sendArgs))
				line = strings.TrimSpace(rest[dec.InputOffset():])
			}
			switch {
			case line == "" && len(sent) > 0:
				reply = strings.Join(sent, "\n")
			case line == "":
				// a bare "!ctx <n>" line keeps the echo reply
			case line == "!crash":
				out.Flush()
				os.Exit(2)
			case line == "!lost":
				// mid-turn session loss, exactly as the real CLI reports it
				out.Flush()
				fmt.Fprintf(os.Stderr, "No conversation found with session ID: %s\n", id)
				os.Exit(1)
			case line == "!toolong":
				// what an over-window payload looks like: an errored result
				// with no usage at all, then a clean exit (verified by
				// make e2e-context)
				emit(map[string]any{
					"type": "result", "subtype": "error_during_execution", "is_error": true,
					"total_cost_usd": 0, "duration_ms": 5, "num_turns": state.Turns,
					"result": "Prompt is too long", "session_id": id,
					"usage": map[string]any{
						"input_tokens": 0, "output_tokens": 0,
						"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
					},
				})
				continue
			case line == "!sysprompt":
				reply = systemPrompt
			case strings.HasPrefix(line, "!huge "):
				n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "!huge ")))
				reply = strings.Repeat("x", n)
			case strings.HasPrefix(line, "!hang "):
				s, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "!hang ")))
				time.Sleep(time.Duration(s) * time.Second)
				reply = "hung " + strconv.Itoa(s) + "s"
			default:
				reply = line
			}
		}

		if partials {
			emit(map[string]any{
				"type": "stream_event",
				"event": map[string]any{
					"type":  "content_block_delta",
					"delta": map[string]any{"type": "text_delta", "text": reply},
				},
				"session_id": id,
			})
		}
		// One assistant event per API step carrying that step's own usage;
		// the result sums them, as the real CLI's cumulative usage does.
		for i := 1; i <= steps; i++ {
			stepText := reply
			if i < steps {
				stepText = fmt.Sprintf("step %d of %d", i, steps)
			}
			emit(map[string]any{
				"type": "assistant",
				"message": map[string]any{
					"role":    "assistant",
					"content": []map[string]any{{"type": "text", "text": stepText}},
					"usage":   usage(ctxTokens, 1),
				},
				"session_id": id,
			})
		}
		emit(map[string]any{
			"type": "result", "subtype": "success", "is_error": false,
			"total_cost_usd": 0.001, "duration_ms": 5, "num_turns": state.Turns,
			"result": reply, "usage": usage(ctxTokens, steps), "session_id": id,
		})

		b, _ := json.Marshal(state)
		if err := os.WriteFile(filepath.Join(stateDir, id+".json"), b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: persist session: %v\n", err)
		}
	}
	// stdin closed: clean exit, like the real CLI.
}

func loadScript() []string {
	data, err := os.ReadFile(".fakeclaude")
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// usage reports token usage summed over that many API steps: one step is an
// assistant event's own usage, the turn's step count is the result event's
// cumulative usage. ctxTokens overrides the per-step input count when a
// "!ctx" directive set it, 0 keeps the small default.
func usage(ctxTokens, steps int) map[string]any {
	input := 10
	if ctxTokens > 0 {
		input = ctxTokens
	}
	return map[string]any{
		"input_tokens": input * steps, "output_tokens": 5 * steps,
		"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
	}
}

// initModel is what the init event reports: the model the runner asked for,
// or a name of our own when it asked for none.
func initModel(model string) string {
	if model == "" {
		return "fakeclaude"
	}
	return model
}

// --- the !send directive: a real client of the hub's MCP endpoint ---

// mcpSess is the one connection this process holds, dialed on the first
// !send — the real CLI likewise connects once per session.
var mcpSess *mcp.ClientSession

func mcpSend(flagConfig string, args map[string]any) string {
	sess, err := mcpConnect(flagConfig)
	if err != nil {
		return "send error: " + err.Error()
	}
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "send_message", Arguments: args,
	})
	if err != nil {
		return "send error: " + err.Error()
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	text := strings.Join(parts, " ")
	if res.IsError {
		return "send error: " + text
	}
	return "sent " + text
}

func mcpConnect(flagConfig string) (*mcp.ClientSession, error) {
	if mcpSess != nil {
		return mcpSess, nil
	}
	raw := flagConfig
	if raw == "" {
		raw = os.Getenv("FAKECLAUDE_MCP_CONFIG")
	}
	if raw == "" {
		return nil, errors.New("no mcp config (--mcp-config or FAKECLAUDE_MCP_CONFIG)")
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		data, err := os.ReadFile(raw)
		if err != nil {
			return nil, err
		}
		raw = string(data)
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}
	for _, srv := range cfg.MCPServers {
		if srv.URL == "" {
			continue
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "fakeclaude", Version: "0"}, nil)
		sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint:             srv.URL,
			HTTPClient:           &http.Client{Transport: headerTransport{srv.Headers}},
			DisableStandaloneSSE: true,
			MaxRetries:           -1,
		}, nil)
		if err != nil {
			return nil, err
		}
		mcpSess = sess
		return sess, nil
	}
	return nil, errors.New("mcp config names no server with a url")
}

type headerTransport struct{ headers map[string]string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}
