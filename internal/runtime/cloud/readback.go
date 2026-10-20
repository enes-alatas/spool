package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/enes-alatas/spool/internal/claude"
)

// StopPost is one post of the loop home's Stop hook: the hook's input, and
// the transcript's lines since the last post that landed (ADR-0059). It is
// the loop's own word, so it drives nothing but its turn's read-back.
type StopPost struct {
	Hook  []byte
	Lines []byte
}

// stopHook is what the hub reads of a Stop hook's input.
type stopHook struct {
	LastAssistantMessage *string `json:"last_assistant_message"`
}

// transcriptLine is what the hub reads of one line of the CLI's
// transcript. Only user and assistant lines become events.
type transcriptLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

// transcriptMessage is the API message a user or assistant line carries.
type transcriptMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *claude.Usage   `json:"usage"`
}

// turnEvents turns a Stop post into the stream-json events the runner reads
// of a turn: an init naming the hub's session and the model the turn ran
// on, the assistant and tool-result events, and a result. The result's
// text is the hook's last_assistant_message, and its usage is the turn's
// API calls summed: the CLI writes one line per content block, each
// repeating its message's usage, so a message counts once. A line the hub
// can't read fails the whole post, as the transcript's format is the CLI's
// and not a published one (ADR-0059): a changed format must be loud. So does
// a post over its bounds.
func turnEvents(post StopPost, hubSession string, bounds Bounds) ([]claude.Event, error) {
	bounds = bounds.withDefaults()
	if len(post.Hook)+len(post.Lines) > bounds.Bytes {
		return nil, fmt.Errorf("the turn's Stop post is over its bound of %d bytes", bounds.Bytes)
	}
	var hook stopHook
	if err := json.Unmarshal(post.Hook, &hook); err != nil {
		return nil, fmt.Errorf("the Stop hook's input can't be read: %w", err)
	}
	var (
		events   []claude.Event
		model    string
		lastText string
		usage    claude.Usage
		counted  = map[string]bool{}
		calls    int
	)
	session, _ := json.Marshal(hubSession)
	lines := bytes.Split(post.Lines, []byte("\n"))
	if len(lines) > bounds.Events {
		return nil, fmt.Errorf("the turn's Stop post is over its bound of %d lines", bounds.Events)
	}
	for number, raw := range lines {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var line transcriptLine
		if err := json.Unmarshal(raw, &line); err != nil || line.Type == "" {
			return nil, fmt.Errorf("transcript line %d can't be read: %s", number+1, snippet(raw))
		}
		if (line.Type != "user" && line.Type != "assistant") || line.IsSidechain || line.IsMeta {
			continue
		}
		var message transcriptMessage
		if err := json.Unmarshal(line.Message, &message); err != nil {
			return nil, fmt.Errorf("transcript line %d has a message that can't be read: %s", number+1, snippet(raw))
		}
		if line.Type == "user" && !bytes.HasPrefix(bytes.TrimSpace(message.Content), []byte("[")) {
			// the wake's own text: the stream-json output echoes no prompt
			continue
		}
		if line.Type == "assistant" {
			if model == "" {
				model = message.Model
			}
			if message.Usage != nil && (message.ID == "" || !counted[message.ID]) {
				counted[message.ID] = true
				calls++
				usage.InputTokens += message.Usage.InputTokens
				usage.OutputTokens += message.Usage.OutputTokens
				usage.CacheCreationTokens += message.Usage.CacheCreationTokens
				usage.CacheReadTokens += message.Usage.CacheReadTokens
			}
		}
		event, err := json.Marshal(map[string]any{
			"type":               line.Type,
			"uuid":               line.UUID,
			"message":            line.Message,
			"session_id":         json.RawMessage(session),
			"parent_tool_use_id": nil,
		})
		if err != nil {
			return nil, err
		}
		decoded := claude.DecodeEvent(event)
		if decoded.Assistant != nil && decoded.Assistant.Text != "" {
			lastText = decoded.Assistant.Text
		}
		events = append(events, decoded)
	}
	if hook.LastAssistantMessage != nil {
		lastText = *hook.LastAssistantMessage
	}
	init, err := json.Marshal(map[string]any{
		"type": "system", "subtype": "init", "session_id": hubSession, "model": model,
	})
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(map[string]any{
		"type":       "result",
		"subtype":    "success",
		"is_error":   false,
		"result":     lastText,
		"session_id": hubSession,
		"num_turns":  calls,
		"usage":      usage,
	})
	if err != nil {
		return nil, err
	}
	return append(append([]claude.Event{claude.DecodeEvent(init)}, events...), claude.DecodeEvent(result)), nil
}
