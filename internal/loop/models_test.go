package loop

import "testing"

func TestContextLimit(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"claude-opus-5", 1_000_000},
		{"claude-opus-5-5", 1_000_000},
		{"claude-fable-5-1", 1_000_000},
		{"claude-fable-5-2", 1_000_000}, // the operator's figure, listed in its own right
		{"claude-sonnet-5", 1_000_000},
		{"claude-haiku-4-5", 200_000},
		{"claude-haiku-4-5-20251001", 200_000}, // dated snapshot of a known id
		{"anthropic.claude-opus-5", 1_000_000}, // vendor-prefixed id
		{"CLAUDE-OPUS-5", 1_000_000},           // case is the CLI's business
		{"opus", 1_000_000},                    // alias, matched exactly
		{"haiku", 1_000_000},                   // alias resolves to claude-haiku-5-5 on 2.1.327
		{"fable", 1_000_000},                   // alias resolves to claude-fable-5-2 on 2.1.327
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
		{"claude-fable-5-3", 0},                     // unlisted point release, not claude-fable-5-2
		{"claude-opus-5-20260410", 1_000_000},       // dated snapshot of a known id
		{"claude-opus-5@20260410", 1_000_000},       // Vertex names the snapshot with @
		{"us.anthropic.claude-opus-5-5", 1_000_000}, // vendor prefix, listed id at the end
		// Bedrock inference profiles append a revision after the snapshot
		// date; that is still the same dated model.
		{"anthropic.claude-opus-5-20260410-v1:0", 1_000_000},
		// A dated snapshot of a two-part id is that id: with the date off,
		// claude-opus-5-5 doesn't end in claude-opus-5, so only one listed
		// id is a suffix here.
		{"claude-opus-5-5-20260410", 1_000_000},
		{"claude-fable-5-3-20260410", 0}, // the issue's bug, with a date on it too
		// The 1M badge comes off before any matching, on a listed id and on
		// an alias alike.
		{"claude-opus-5[1m]", 1_000_000},
		{"claude-opus-5-20260410[1m]", 1_000_000},
		{"sonnet[1m]", 1_000_000},
	}
	for _, testCase := range cases {
		if got := ContextLimit(testCase.model); got != testCase.want {
			t.Errorf("ContextLimit(%q) = %d, want %d", testCase.model, got, testCase.want)
		}
	}
}
