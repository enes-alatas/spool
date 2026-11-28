package eval

import (
	"strings"
	"testing"
)

func TestAReplayIsReadAsARecordedTurnIs(t *testing.T) {
	original := Case{
		ID: "c1", Loop: "terra", Trigger: "message", Pacing: "self", MinWakeSec: 300, MaxWakeSec: 14400,
		Recipients: []string{"quinn"}, ChannelLoops: map[string][]string{"fleet": {"quinn"}},
		Input: []string{"[message from @quinn (loop) · channel:fleet · ref:5]\n\n@terra status?"},
	}
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u1","name":"mcp__spool__send_message","input":{"destination":"channel:fleet","text":"PR ready","reply_to":"ref:5"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"{\"message_id\":900001,\"ref\":\"ref:900001\"}"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u2","name":"` + StubBashTool + `","input":{"command":"gh pr comment 9 --body \"hi @` + `quinn\""}}]}}`,
		`{"type":"result","result":"done\n[next-wake: 45m]","total_cost_usd":0.0123}`,
	}, "\n")
	replayed, cost, err := Replayed(original, []byte(stream))
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0.0123 {
		t.Errorf("cost = %v, want the result's total_cost_usd", cost)
	}
	if replayed.ID != "c1" || replayed.Trigger != "message" || replayed.ChannelLoops["fleet"][0] != "quinn" {
		t.Errorf("the original's facts were not kept: %+v", replayed)
	}
	if len(replayed.Sends) != 1 || replayed.Sends[0].Ref != "ref:900001" {
		t.Errorf("sends = %+v, want the one send with its stub ref", replayed.Sends)
	}
	if len(replayed.GitHubWrites) != 1 {
		t.Fatalf("github writes = %+v, want the stub Bash read as Bash", replayed.GitHubWrites)
	}
	if !strings.Contains(replayed.FinalText, "[next-wake: 45m]") {
		t.Errorf("final text = %q", replayed.FinalText)
	}
	// The graders read it like any case: the comment's mention is a slip.
	if verdict := gradeGitHubNoMention(replayed); verdict.Pass {
		t.Error("a replayed mention on GitHub passed")
	}
}

func TestAReplayThatNeverEndsItsTurnIsNotGraded(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[]}}`
	if _, _, err := Replayed(Case{Input: []string{"[tick]"}}, []byte(stream)); err == nil {
		t.Error("a replay with no result was read as a case")
	}
}
