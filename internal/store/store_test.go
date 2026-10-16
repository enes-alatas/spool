package store

import "testing"

// A loop's surface is read off the credential it holds, so the two cannot
// disagree (#230).
func TestLoopSurface(t *testing.T) {
	for _, testCase := range []struct {
		name string
		loop Loop
		want string
	}{
		{"none", Loop{}, ""},
		{"telegram", Loop{TGBotToken: "tg-synthetic"}, SurfaceTelegram},
		{"slack", Loop{SlackBotToken: "xoxb-synthetic", SlackAppToken: "xapp-synthetic"}, SurfaceSlack},
	} {
		if got := testCase.loop.Surface(); got != testCase.want {
			t.Errorf("%s: Surface() = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// An access frame carries no pairing code, whatever the row holds: the
// stream reaches members, who read none. The row itself keeps its code.
func TestAccessFramesCarryNoPairingCode(t *testing.T) {
	tg := &TGSender{TGUserID: 4242, Status: SenderPending, PairCode: "654321"}
	if frame := tg.Frame(); frame.PairCode != "" || frame.Surface != SurfaceTelegram {
		t.Errorf("a Telegram sender's frame = %+v; want no code, marked telegram", frame)
	}
	slack := &SlackSender{SlackUserID: "U0ALICE", Status: SenderPending, PairCode: "123456"}
	if frame := slack.Frame(); frame.PairCode != "" || frame.Surface != SurfaceSlack {
		t.Errorf("a Slack sender's frame = %+v; want no code, marked slack", frame)
	}
	if tg.PairCode != "654321" || slack.PairCode != "123456" {
		t.Errorf("framing changed the rows: %q, %q", tg.PairCode, slack.PairCode)
	}
}
