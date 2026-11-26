package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// StubBashTool is the name the CLI gives the replay stub's shell tool. A
// replayed turn has no built-in tools, so nothing it runs can reach GitHub,
// git or the workstation; the stub records each command and answers it
// with no output. The extractor reads it as Bash.
const StubBashTool = "mcp__shell__Bash"

// Replayed is the case a model's replay of original produced, read from the
// CLI's stream-json output exactly as a recorded turn is read from the
// store, so the same graders score both. The loop facts (pacing, wake
// range, who a message reaches) are the original's. ShownRefs are the ones
// the replay itself was shown: its input and its own sends' results.
//
// cost is the CLI's total_cost_usd for the run.
func Replayed(original Case, stream []byte) (replayed Case, cost float64, err error) {
	events := make([]*store.Event, 0, len(original.Input)+16)
	for _, text := range original.Input {
		payload, _ := json.Marshal(map[string]string{"trigger": original.Trigger, "text": text})
		events = append(events, &store.Event{SessionID: "replay", TurnID: "replay", Type: "envelope", Payload: string(payload)})
	}
	scanner := bufio.NewScanner(bytes.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		var head struct {
			Type         string  `json:"type"`
			TotalCostUSD float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal(line, &head) != nil || head.Type == "" {
			continue
		}
		if head.Type == "result" {
			cost = head.TotalCostUSD
		}
		payload := strings.ReplaceAll(string(line), `"name":"`+StubBashTool+`"`, `"name":"Bash"`)
		events = append(events, &store.Event{SessionID: "replay", TurnID: "replay", Type: head.Type, Payload: payload})
	}
	if err := scanner.Err(); err != nil {
		return Case{}, cost, err
	}
	cases := Extract(events, LoopInfo{
		Name:         original.Loop,
		Pacing:       original.Pacing,
		MinWakeSec:   original.MinWakeSec,
		MaxWakeSec:   original.MaxWakeSec,
		Recipients:   original.Recipients,
		ChannelLoops: original.ChannelLoops,
		People:       original.People,
	}, func(text string) string { return text })
	if len(cases) != 1 {
		return Case{}, cost, errors.New("the replay never finished its turn")
	}
	replayed = cases[0]
	replayed.ID = original.ID
	replayed.Slip = original.Slip
	return replayed, cost, nil
}
