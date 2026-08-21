package loop

import "strings"

// Context windows, in tokens, of the models a loop can run on, keyed by the
// id the CLI reports. A model we don't know returns 0, which callers must
// read as "unknown" rather than "zero": the control room then shows absolute
// tokens with no percentage rather than a ratio against a guess.
var contextLimits = map[string]int{
	"claude-fable-5":    1_000_000,
	"claude-mythos-5":   1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-4-5":  200_000,
}

// aliasLimits covers the short names an operator may configure a loop with.
// They are matched exactly and never as substrings: an alias floats between
// releases, so "opus" says nothing about an id it merely appears inside —
// claude-opus-4-1 is a 200k model, and treating it as a megatoken one would
// under-report a nearly full context as barely used.
var aliasLimits = map[string]int{
	"fable":  1_000_000,
	"opus":   1_000_000,
	"sonnet": 1_000_000,
	"haiku":  200_000,
}

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
	// full id contained in the reported string; nothing else counts.
	best, bestLen := 0, 0
	for id, limit := range contextLimits {
		if len(id) > bestLen && strings.Contains(model, id) {
			best, bestLen = limit, len(id)
		}
	}
	return best
}
