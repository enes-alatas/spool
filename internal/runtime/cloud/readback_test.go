package cloud

import (
	"math"
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
	// claude-opus-5-5 at $4 input, $20 output, $5 cache writes and $0.20
	// cache reads per million tokens
	if want := (30*4 + 13*20 + 100*5 + 2100*0.20) / 1e6; !result.CostEstimated || result.TurnCostUSD == nil ||
		math.Abs(*result.TurnCostUSD-want) > 1e-12 {
		t.Errorf("result cost is %v (estimated %v), want the estimate %v", result.TurnCostUSD, result.CostEstimated, want)
	}
}

// Each call is priced at its own model and prompt length, a model the hub
// has no price for leaves the turn's cost unknown, and a one-hour cache
// write is priced as one.
func TestTurnEventsPriceEachCallByItsModel(t *testing.T) {
	cost := func(lines string) *float64 {
		t.Helper()
		events, err := turnEvents(StopPost{Hook: []byte(`{}`), Lines: []byte(lines)}, "s", Bounds{})
		if err != nil {
			t.Fatal(err)
		}
		result := events[len(events)-1].Result
		if !result.CostEstimated {
			t.Errorf("a cloud turn's cost isn't marked as an estimate: %+v", result)
		}
		return result.TurnCostUSD
	}
	if got := cost(strings.ReplaceAll(transcriptTurn, "claude-opus-5-5", "claude-unpriced-9")); got != nil {
		t.Errorf("an unpriced model's turn costs %v, want unknown", *got)
	}
	hour := `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":300,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200}}}}`
	// claude-haiku-4-5 at $1 input: five-minute writes at 1.25 times it,
	// one-hour writes at twice it
	if got, want := cost(hour), (100*1.25+200*2)/1e6; got == nil || math.Abs(*got-want) > 1e-12 {
		t.Errorf("one-hour cache writes cost %v, want %v", got, want)
	}
	// claude-haiku-5-5 bills a call whose prompt is over 100K tokens at
	// $0.50 input and $2.50 output, and one at or under it at $0.10 and
	// $0.50: each call is priced on its own prompt, not the turn's sum
	haiku := `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","model":"claude-haiku-5-5","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":1000,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":100000}}}
{"type":"assistant","uuid":"a2","message":{"id":"msg_2","model":"claude-haiku-5-5","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":1000,"output_tokens":100,"cache_creation_input_tokens":0,"cache_read_input_tokens":99000}}}`
	long := 1000*0.50 + 100*2.50 + 100000*0.05
	short := 1000*0.10 + 100*0.50 + 99000*0.01
	if got, want := cost(haiku), (long+short)/1e6; got == nil || math.Abs(*got-want) > 1e-12 {
		t.Errorf("a long and a short claude-haiku-5-5 call cost %v, want %v", got, want)
	}
	// a turn that changes model prices each call at the model it ran on
	switched := `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":1000,"output_tokens":100}}}
{"type":"assistant","uuid":"a2","message":{"id":"msg_2","model":"claude-sonnet-5-5","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":1000,"output_tokens":100}}}`
	if got, want := cost(switched), (1000*4+100*20+1000*2+100*10)/1e6; got == nil || math.Abs(*got-want) > 1e-12 {
		t.Errorf("a turn on two models costs %v, want %v", got, want)
	}
	if got := cost(strings.Replace(switched, "claude-sonnet-5-5", "claude-unpriced-9", 1)); got != nil {
		t.Errorf("a turn with one unpriced call costs %v, want unknown", *got)
	}
}

// The token counts are the session's word: a negative one would price to
// a negative cost, so the line is refused as unreadable.
func TestTurnEventsRefuseANegativeTokenCount(t *testing.T) {
	for name, field := range map[string]string{
		"output tokens":    `"output_tokens":-50000000`,
		"a one-hour write": `"cache_creation":{"ephemeral_1h_input_tokens":-5}`,
	} {
		line := `{"type":"assistant","uuid":"a1","message":{"id":"msg_1","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],"usage":{` + field + `}}}`
		if _, err := turnEvents(StopPost{Hook: []byte(`{}`), Lines: []byte(line)}, "s", Bounds{}); err == nil {
			t.Errorf("%s: a negative count was read", name)
		}
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
