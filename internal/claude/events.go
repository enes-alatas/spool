package claude

import (
	"encoding/json"
	"strings"
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
	SessionID  string      `json:"session_id"`
	Cwd        string      `json:"cwd"`
	Model      string      `json:"model"`
	MCPServers []MCPServer `json:"mcp_servers"`
	// Tools names every tool the session can call: the CLI's built-ins,
	// and each MCP server's as mcp__<server>__<tool>.
	Tools []string `json:"tools"`
}

// MCPTools counts the session's tools that came from an MCP server, or from
// the named one when server is not "" (#489).
func (init *InitInfo) MCPTools(server string) int {
	prefix := "mcp__"
	if server != "" {
		prefix += server + "__"
	}
	count := 0
	for _, tool := range init.Tools {
		if strings.HasPrefix(tool, prefix) {
			count++
		}
	}
	return count
}

// MCPServer is one MCP server as the CLI reports it at init: its name in
// --mcp-config, and whether it connected ("connected", "failed", ...).
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// SpoolMCPFailure is the status the CLI gave the hub's MCP server when it
// did not connect, and empty when it did or the process has none: without
// it the loop runs its turns unable to send anything (#476). Pending is not
// a failure; the CLI may still be connecting.
func (init *InitInfo) SpoolMCPFailure() string {
	for _, server := range init.MCPServers {
		if server.Name == SpoolMCPServer && server.Status != "connected" && server.Status != "pending" {
			return server.Status
		}
	}
	return ""
}

// AssistantInfo carries the API message of an assistant event. Content is kept
// raw for storage/UI; Text is the concatenation of its text blocks. Usage is
// this one API call's usage — unlike the result event's, which sums every
// call of the turn and says nothing about context occupancy. Error is set
// when the CLI stands in for a call the API refused: the message is then the
// CLI's own, naming the failure, not the model's.
type AssistantInfo struct {
	Text    string
	Content json.RawMessage
	Usage   Usage
	Error   string
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
	// APIErrorStatus is the HTTP status of the API call the turn failed on,
	// 0 when it failed on none.
	APIErrorStatus int `json:"api_error_status"`
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
			Error string `json:"error"`
		}
		if json.Unmarshal(line, &body) == nil {
			ev.Assistant = &AssistantInfo{
				Text:    textFromContent(body.Message.Content),
				Content: body.Message.Content,
				Usage:   body.Message.Usage,
				Error:   body.Error,
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
	for _, block := range blocks {
		if block.Type == "text" {
			out += block.Text
		}
	}
	return out
}
