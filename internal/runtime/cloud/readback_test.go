package cloud

import (
	"strings"
	"testing"
)

// A turn's transcript as the CLI writes it, in the shape run E6 recorded
// (#957): the wake's text, an assistant message split into a tool-use line
// and a text line that repeat one usage, the tool's result, and the reply.
// Its ids and text are synthetic.
const transcriptTurn = `{"type":"user","uuid":"u1","message":{"role":"user","content":"[wake] tick"}}
{"type":"assistant","uuid":"a1","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"text","text":"Checking."}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}}
{"type":"assistant","uuid":"a2","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"date"}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}}
{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"Tue"}]}}
{"type":"file-history-snapshot","messageId":"m"}
{"type":"assistant","uuid":"a3","isSidechain":true,"message":{"id":"msg_s","model":"claude-haiku-5-5","content":[{"type":"text","text":"sub"}],"usage":{"input_tokens":7,"output_tokens":7}}}
{"type":"assistant","uuid":"a4","message":{"id":"msg_2","model":"claude-opus-5-5","content":[{"type":"text","text":"It is Tuesday."}],"usage":{"input_tokens":20,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":1100}}}
`

func TestTurnEventsReadATurnBackFromItsTranscript(t *testing.T) {
	events, err := turnEvents(StopPost{
		Hook:  []byte(`{"hook_event_name":"Stop","last_assistant_message":"It is Tuesday."}`),
		Lines: []byte(transcriptTurn),
	}, "hub-session-1", Bounds{})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
		if event.SessionID != "hub-session-1" {
			t.Errorf("a %s event carries session %q, want the hub's", event.Type, event.SessionID)
		}
	}
	if got := strings.Join(types, " "); got != "system assistant assistant user assistant result" {
		t.Fatalf("the turn reads back as %q", got)
	}
	init := events[0].Init
	if init == nil || init.Model != "claude-opus-5-5" || init.SessionID != "hub-session-1" {
		t.Errorf("init is %+v, want the hub's session on the turn's model", init)
	}
	if events[1].Assistant == nil || events[1].Assistant.Usage.CacheReadTokens != 1000 {
		t.Errorf("an assistant event lost its call's usage: %+v", events[1].Assistant)
	}
	result := events[len(events)-1].Result
	if result == nil || result.IsError || result.ResultText != "It is Tuesday." || result.NumTurns != 2 {
		t.Fatalf("result is %+v, want the reply over two API calls", result)
	}
	if want := (struct{ in, out, created, read int }{30, 13, 100, 2100}); result.Usage.InputTokens != want.in ||
		result.Usage.OutputTokens != want.out || result.Usage.CacheCreationTokens != want.created || result.Usage.CacheReadTokens != want.read {
		t.Errorf("result usage is %+v, want each message's counted once and the sidechain's left out", result.Usage)
	}
}

func TestTurnEventsTakeTheTextFromTheTranscriptWithoutTheHooks(t *testing.T) {
	events, err := turnEvents(StopPost{Hook: []byte(`{}`), Lines: []byte(transcriptTurn)}, "s", Bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if text := events[len(events)-1].Result.ResultText; text != "It is Tuesday." {
		t.Errorf("result text is %q, want the last assistant text", text)
	}
}

func TestTurnEventsRefuseWhatTheyCantRead(t *testing.T) {
	for name, post := range map[string]StopPost{
		"a line that isn't JSON":   {Hook: []byte(`{}`), Lines: []byte("{\"type\":\"x\"}\nnot json\n")},
		"a line with no type":      {Hook: []byte(`{}`), Lines: []byte(`{"uuid":"x"}`)},
		"a message that isn't one": {Hook: []byte(`{}`), Lines: []byte(`{"type":"assistant","message":"hi"}`)},
		"a hook input":             {Hook: []byte(`not json`)},
		"over the byte bound":      {Hook: []byte(`{}`), Lines: []byte(strings.Repeat("x", 64))},
		"over the line bound":      {Hook: []byte(`{}`), Lines: []byte(strings.Repeat("{\"type\":\"x\"}\n", 5))},
	} {
		if _, err := turnEvents(post, "s", Bounds{Events: 4, Bytes: 63}); err == nil {
			t.Errorf("%s: the post was read", name)
		}
	}
}
