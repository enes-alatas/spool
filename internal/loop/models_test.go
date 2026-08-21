package loop

import "testing"

func TestContextLimit(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"claude-opus-5", 1_000_000},
		{"claude-sonnet-5", 1_000_000},
		{"claude-haiku-4-5", 200_000},
		{"claude-haiku-4-5-20251001", 200_000}, // dated snapshot of a known id
		{"anthropic.claude-opus-5", 1_000_000}, // vendor-prefixed id
		{"CLAUDE-OPUS-5", 1_000_000},           // case is the CLI's business
		{"opus", 1_000_000},                    // alias, matched exactly
		{"", 0},                                // "whatever the CLI resolves to"
		{"fakeclaude", 0},                      // unknown: say so, don't guess
		{"some-other-vendor-model", 0},
		// Known family, id we don't carry: these are 200k models, so
		// matching them against the "opus"/"sonnet" aliases would render a
		// nearly full context as barely used — exactly what the gauge exists
		// to catch.
		{"claude-opus-4-1-20240805", 0},
		{"claude-sonnet-4-5-20250929", 0},
		{"claude-3-7-sonnet-20250219", 0},
	}
	for _, c := range cases {
		if got := ContextLimit(c.model); got != c.want {
			t.Errorf("ContextLimit(%q) = %d, want %d", c.model, got, c.want)
		}
	}
}
