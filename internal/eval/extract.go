package eval

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// sendTool is the name the CLI gives the hub's send_message tool.
const sendTool = "mcp__spool__send_message"

// LoopInfo is what a case needs to know about the loop that ran it.
type LoopInfo struct {
	Name       string
	Pacing     string
	MinWakeSec int
	MaxWakeSec int
	Recipients []string
}

var refPattern = regexp.MustCompile(`ref:\d+`)

// Extract turns one loop's events, oldest first, into cases: one per turn.
// A turn that never finished — killed, or cut off by a restart — has no
// reply to grade and is left out. Turns the operator started privately — an owner DM, a control-room
// message — are left out whole, and any owner-DM send in the rest keeps its
// destination but not its text. Every string that leaves is passed through
// clean first.
func Extract(events []*store.Event, loop LoopInfo, clean func(string) string) []Case {
	var (
		cases   []*Case
		byTurn  = map[string]*Case{}
		private = map[string]bool{}
		// finished marks a turn that reached its result.
		finished = map[string]bool{}
		// shown is every ref each session has been shown, in order.
		shown = map[string][]string{}
		// sealed marks a case whose ShownRefs is final: its envelopes are
		// read, and what follows is the loop's own doing.
		sealed = map[string]bool{}
		// sendByTool finds the send a tool result answers, by index: the
		// slice it points into is still growing.
		sendByTool = map[string]int{}
		// written is what the turn's Write calls put where, so a body
		// passed by file can be graded like an inline one.
		written = map[string]map[string]string{}
	)
	see := func(session, text string) {
		shown[session] = append(shown[session], refPattern.FindAllString(text, -1)...)
	}

	for _, event := range events {
		if event.TurnID == "" {
			continue
		}
		turnCase, ok := byTurn[event.TurnID]
		if !ok {
			turnCase = &Case{
				ID:         event.TurnID,
				Loop:       loop.Name,
				Pacing:     loop.Pacing,
				Recipients: loop.Recipients,
				MinWakeSec: loop.MinWakeSec,
				MaxWakeSec: loop.MaxWakeSec,
			}
			byTurn[event.TurnID] = turnCase
			written[event.TurnID] = map[string]string{}
			cases = append(cases, turnCase)
		}

		if event.Type == "envelope" {
			var envelope struct {
				Trigger      string `json:"trigger"`
				Text         string `json:"text"`
				Conversation string `json:"conversation"`
			}
			if json.Unmarshal([]byte(event.Payload), &envelope) != nil {
				continue
			}
			if envelope.Conversation == "owner_dm" || envelope.Conversation == "control_room" {
				private[event.TurnID] = true
			}
			if turnCase.Trigger == "" {
				turnCase.Trigger = envelope.Trigger
			}
			turnCase.Input = append(turnCase.Input, envelope.Text)
			see(event.SessionID, envelope.Text)
			continue
		}
		if !sealed[event.TurnID] {
			turnCase.ShownRefs = append([]string(nil), shown[event.SessionID]...)
			sealed[event.TurnID] = true
		}

		var line struct {
			Type    string `json:"type"`
			Result  string `json:"result"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(event.Payload), &line) != nil {
			continue
		}
		switch line.Type {
		case "result":
			turnCase.FinalText = line.Result
			finished[event.TurnID] = true
		case "assistant":
			var blocks []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(line.Message.Content, &blocks) != nil {
				continue
			}
			for _, block := range blocks {
				if block.Type != "tool_use" {
					continue
				}
				switch block.Name {
				case sendTool:
					var send Send
					if json.Unmarshal(block.Input, &send) != nil {
						continue
					}
					sendByTool[block.ID] = len(turnCase.Sends)
					turnCase.Sends = append(turnCase.Sends, send)
				case "Write":
					var write struct {
						Path    string `json:"file_path"`
						Content string `json:"content"`
					}
					if json.Unmarshal(block.Input, &write) == nil {
						written[event.TurnID][write.Path] = write.Content
					}
				case "Bash":
					var bash struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(block.Input, &bash) != nil || !writeStart.MatchString(bash.Command) {
						continue
					}
					write := GitHubWrite{Command: bash.Command}
					for path, body := range written[event.TurnID] {
						if strings.Contains(bash.Command, path) {
							if write.Files == nil {
								write.Files = map[string]string{}
							}
							write.Files[path] = body
						}
					}
					turnCase.GitHubWrites = append(turnCase.GitHubWrites, write)
				}
			}
		case "user":
			var blocks []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if json.Unmarshal(line.Message.Content, &blocks) != nil {
				continue
			}
			for _, block := range blocks {
				index, ok := sendByTool[block.ToolUseID]
				if block.Type != "tool_result" || !ok || index >= len(turnCase.Sends) {
					continue
				}
				var text string
				if json.Unmarshal(block.Content, &text) != nil {
					continue
				}
				var result struct {
					Ref string `json:"ref"`
				}
				if json.Unmarshal([]byte(text), &result) == nil && result.Ref != "" {
					turnCase.Sends[index].Ref = result.Ref
					shown[event.SessionID] = append(shown[event.SessionID], result.Ref)
				}
			}
		}
	}

	var out []Case
	for _, turnCase := range cases {
		if private[turnCase.ID] || !finished[turnCase.ID] || len(turnCase.Input) == 0 {
			continue
		}
		out = append(out, cleanCase(*turnCase, clean))
	}
	return out
}

func cleanCase(turnCase Case, clean func(string) string) Case {
	for i := range turnCase.Input {
		turnCase.Input[i] = clean(turnCase.Input[i])
	}
	sends := make([]Send, len(turnCase.Sends))
	for i, send := range turnCase.Sends {
		if send.Destination == "owner_dm" || send.Destination == "control_room" {
			send.Text, send.Withheld = "", true
		}
		send.Text = clean(send.Text)
		sends[i] = send
	}
	turnCase.Sends = sends
	writes := make([]GitHubWrite, len(turnCase.GitHubWrites))
	for i, write := range turnCase.GitHubWrites {
		cleaned := GitHubWrite{Command: clean(write.Command)}
		for path, body := range write.Files {
			if cleaned.Files == nil {
				cleaned.Files = map[string]string{}
			}
			cleaned.Files[path] = clean(body)
		}
		writes[i] = cleaned
	}
	turnCase.GitHubWrites = writes
	turnCase.FinalText = clean(turnCase.FinalText)
	return turnCase
}
