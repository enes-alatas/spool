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

// A loop's Claude login is its own, else its team's for a contained loop,
// else the hub's default; a bare loop runs on the host's, and a cloud loop
// does not inherit its team's (ADR-0060, ADR-0066).
func TestALoopsLoginResolvesFromLoopToTeamToHub(t *testing.T) {
	cases := []struct {
		name                string
		loopRecord          Loop
		wantLogin, wantFrom string
	}{
		{"contained, its own", Loop{Runtime: RuntimeDocker, ClaudeLogin: "ada", TeamClaudeLogin: "bob"}, "ada", LoginFromLoop},
		{"contained, its team's", Loop{Runtime: RuntimeDocker, TeamClaudeLogin: "bob"}, "bob", LoginFromTeam},
		{"contained, the hub's", Loop{Runtime: RuntimeDocker}, "", LoginFromHub},
		{"cloud, not its team's", Loop{Runtime: RuntimeCloud, TeamClaudeLogin: "bob"}, "", LoginFromHub},
		{"cloud, its own", Loop{Runtime: RuntimeCloud, ClaudeLogin: "ada"}, "ada", LoginFromLoop},
		{"bare", Loop{Runtime: RuntimeBare, TeamClaudeLogin: "bob"}, "", LoginFromHost},
		{"bare, a login set in the store", Loop{Runtime: RuntimeBare, ClaudeLogin: "ada"}, "ada", LoginFromLoop},
	}
	for _, testCase := range cases {
		if login, from := testCase.loopRecord.Login(); login != testCase.wantLogin || from != testCase.wantFrom {
			t.Errorf("%s: Login() = %q, %q; want %q, %q", testCase.name, login, from, testCase.wantLogin, testCase.wantFrom)
		}
	}
}
