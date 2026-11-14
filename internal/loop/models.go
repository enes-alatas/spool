package loop

import (
	"regexp"
	"strings"
)

// Context windows, in tokens, of the models a loop can run on, keyed by the
// id the CLI reports. A model we don't know returns 0, which callers must
// read as "unknown" rather than "zero": the control room then shows absolute
// tokens with no percentage rather than a ratio against a guess.
var contextLimits = map[string]int{
	// No source states claude-fable-5-2's window; 1M is the operator's
	// figure, given on 2026-11-14 (#1591).
	"claude-fable-5-2":  1_000_000,
	"claude-fable-5-1":  1_000_000,
	"claude-fable-5":    1_000_000,
	"claude-mythos-5":   1_000_000,
	"claude-opus-5-5":   1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5-5": 1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-5-5":  1_000_000,
	"claude-haiku-4-5":  200_000,
}

// aliasLimits covers the short names an operator may configure a loop with.
// They are matched exactly and never as substrings: an alias floats between
// releases, so "opus" says nothing about an id it merely appears inside —
// claude-opus-4-1 is a 200k model, and treating it as a megatoken one would
// under-report a nearly full context as barely used.
var aliasLimits = map[string]int{
	// "fable" resolves to claude-fable-5-2 on 2.1.322, and answers its
	// window, the operator's figure (#1591).
	"fable":  1_000_000,
	"opus":   1_000_000,
	"sonnet": 1_000_000,
	"haiku":  1_000_000,
}

// datedSuffix is a snapshot date, -YYYYMMDD, optionally followed by a
// vendor revision such as -v1:0 (<prefix>.claude-<id>-YYYYMMDD-v1:0).
var datedSuffix = regexp.MustCompile(`-\d{8}(-v\d+(:\d+)?)?$`)

// contextMarker is the 1M-context badge a CLI may append to the id it
// reports (claude-opus-5[1m]). No recorded init carries one — every probe
// cmd/fakeclaude/FIDELITY.md lists reported a bare id — but if one ever
// arrives it must not blank the loop's window: the badge rides on the id
// the tables key on, so it comes off before matching.
var contextMarker = regexp.MustCompile(`\[1m\]$`)

// ContextLimit returns the context window of model in tokens, or 0 when the
// model is unknown — including the empty string, which means "whatever the
// CLI resolves to" and is only knowable once a turn has run under it. A
// model from a known family that isn't listed is unknown too: families span
// different window sizes, so guessing from the name is worse than admitting
// we don't know.
func ContextLimit(model string) int {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return 0
	}
	if limit, ok := contextLimits[model]; ok {
		return limit
	}
	if limit, ok := aliasLimits[model]; ok {
		return limit
	}
	// Dated snapshots and vendor-prefixed ids carry a full id inside them —
	// claude-opus-5-20260410, anthropic.claude-opus-5. Match the longest
	// full id that ends the string, once a snapshot date is taken off (a
	// date optionally followed by a vendor revision such as -v1:0): an
	// unlisted point release (claude-fable-5-2) is a different model and
	// must stay unknown.
	base := contextMarker.ReplaceAllString(model, "")
	base = datedSuffix.ReplaceAllString(base, "")
	best, bestLen := 0, 0
	for id, limit := range contextLimits {
		if len(id) > bestLen && strings.HasSuffix(base, id) {
			best, bestLen = limit, len(id)
		}
	}
	return best
}
