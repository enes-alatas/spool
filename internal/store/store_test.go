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
