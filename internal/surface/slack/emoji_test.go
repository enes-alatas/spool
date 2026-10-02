package slack

import "testing"

// A Slack name and the emoji it draws map both ways, skin tones and custom
// emoji included.
func TestEmojiNamesMapBothWays(t *testing.T) {
	for _, testCase := range []struct{ name, emoji string }{
		{"+1", "👍"},
		{"+1::skin-tone-3", "👍🏼"},
		{"heart", "❤\uFE0F"},
		{"tada", "🎉"},
		{"female-technologist::skin-tone-6", "👩🏿\u200D💻"},
		{"flag-tr", "🇹🇷"},
		{"partyparrot", ":partyparrot:"},
	} {
		if got := emojiOf(testCase.name); got != testCase.emoji {
			t.Errorf("emojiOf(%q) = %q, want %q", testCase.name, got, testCase.emoji)
		}
		if got := slackName(testCase.emoji); got != testCase.name {
			t.Errorf("slackName(%q) = %q, want %q", testCase.emoji, got, testCase.name)
		}
	}
	// an alias reads as its emoji, and the emoji sets its primary name;
	// Telegram's ❤ comes without the presentation selector
	if got := emojiOf("thumbsup"); got != "👍" {
		t.Errorf("emojiOf(thumbsup) = %q", got)
	}
	if got := slackName("❤"); got != "heart" {
		t.Errorf("slackName(❤) = %q, want heart", got)
	}
	if got := slackName("🫨\u200D🫨"); got != "" {
		t.Errorf("slackName of no emoji Slack has = %q, want none", got)
	}
}
