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
// repeats). Directives: "!crash" exits 2 mid-turn without a result; "!huge
// <bytes>" replies with that many bytes; "!hang <seconds>" sleeps first.
// Without a script file, every turn echoes: "echo: <received text>".
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type sessionState struct {
	Turns int `json:"turns"`
}

func main() {
	var sessionID, resumeID, model string
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
		case "--input-format", "--output-format", "--permission-mode",
			"--effort", "--append-system-prompt", "--add-dir":
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
		if script != nil {
			line := script[min(state.Turns, len(script))-1]
			switch {
			case line == "!crash":
				out.Flush()
				os.Exit(2)
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
		emit(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": reply}},
				"usage":   usage(),
			},
			"session_id": id,
		})
		emit(map[string]any{
			"type": "result", "subtype": "success", "is_error": false,
			"total_cost_usd": 0.001, "duration_ms": 5, "num_turns": state.Turns,
			"result": reply, "usage": usage(), "session_id": id,
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

func usage() map[string]any {
	return map[string]any{
		"input_tokens": 10, "output_tokens": 5,
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
