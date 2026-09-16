package claude

import (
	"encoding/json"
)

// Event is one decoded stdout line from a claude subprocess. Raw always holds
// the original line; typed fields are populated for the event kinds Spool
// acts on. Unknown types/subtypes pass through untouched (hooks and future
// CLI versions emit system subtypes we must tolerate).
type Event struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Raw       json.RawMessage `json:"-"`

	Init      *InitInfo      `json:"-"`
	Assistant *AssistantInfo `json:"-"`
	Result    *ResultInfo    `json:"-"`
	RateLimit *RateLimitInfo `json:"-"`
}

type InitInfo struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
}

// AssistantInfo carries the API message of an assistant event. Content is kept
// raw for storage/UI; Text is the concatenation of its text blocks. Usage is
// this one API call's usage — unlike the result event's, which sums every
// call of the turn and says nothing about context occupancy.
type AssistantInfo struct {
	Text    string
	Content json.RawMessage
	Usage   Usage
}

type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

type ResultInfo struct {
	IsError    bool    `json:"is_error"`
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	NumTurns   int     `json:"num_turns"`
	ResultText string  `json:"result"`
	Usage      Usage   `json:"usage"`
}

type RateLimitInfo struct {
	Status        string `json:"status"`
	ResetsAt      int64  `json:"resetsAt"`
	RateLimitType string `json:"rateLimitType"`
}

// DecodeEvent parses one stdout line. It never fails hard: undecodable input
// comes back as Type "unparsed" so the caller can store and move on.
func DecodeEvent(line []byte) Event {
	var probe struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	raw := json.RawMessage(append([]byte(nil), line...))
	if err := json.Unmarshal(line, &probe); err != nil || probe.Type == "" {
		return Event{Type: "unparsed", Raw: raw}
	}
	ev := Event{Type: probe.Type, Subtype: probe.Subtype, SessionID: probe.SessionID, Raw: raw}

	switch probe.Type {
	case "system":
		if probe.Subtype == "init" {
			var init InitInfo
			if json.Unmarshal(line, &init) == nil {
				ev.Init = &init
			}
		}
	case "assistant":
		var body struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				Usage   Usage           `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &body) == nil {
			ev.Assistant = &AssistantInfo{
				Text:    textFromContent(body.Message.Content),
				Content: body.Message.Content,
				Usage:   body.Message.Usage,
			}
		}
	case "result":
		var res ResultInfo
		if json.Unmarshal(line, &res) == nil {
			ev.Result = &res
		}
	case "rate_limit_event":
		var body struct {
			Info RateLimitInfo `json:"rate_limit_info"`
		}
		if json.Unmarshal(line, &body) == nil {
			ev.RateLimit = &body.Info
		}
	}
	return ev
}

func textFromContent(content json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}
