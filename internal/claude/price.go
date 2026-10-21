package claude

import "regexp"

// Prices are the hub's own table of what each model's tokens cost, in US
// dollars per million tokens. A cloud turn reports no cost, only its token
// counts, so the hub prices those from this table and marks the result as
// an estimate (ADR-0059). Supporting a new model includes adding its row:
// a model the table lacks is priced as unknown, never as free.
var Prices = map[string]Price{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-mythos-5-1": {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1},
	"claude-mythos-5":   {Input: 10, Output: 50, CacheRead: 1},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20},
	"claude-opus-5":     {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-8":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-7":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-6":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-4-6": {Input: 3, Output: 15, CacheRead: 0.30},
	"claude-haiku-5-5": {Input: 0.10, Output: 0.50, CacheRead: 0.01,
		LongPrompt: &Price{Input: 0.50, Output: 2.50, CacheRead: 0.05}},
	"claude-haiku-4-5": {Input: 1, Output: 5, CacheRead: 0.10},
}

// Price is one model's row, in dollars per million tokens. A cache write
// costs 1.25 times the input price for the five-minute cache and twice it
// for the one-hour cache, on every model. A model with a second rate card
// for long prompts has it in LongPrompt, which prices every token of a
// call whose prompt is over longPromptTokens.
type Price struct {
	Input      float64
	Output     float64
	CacheRead  float64
	LongPrompt *Price
}

// longPromptTokens is the prompt length, counting input, cache reads and
// cache writes, above which a call is priced at its model's LongPrompt
// card.
const longPromptTokens = 100_000

// dateSuffix is the snapshot date an API model ID may carry after the
// model's own ID, as in claude-haiku-4-5-20251001.
var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// EstimateCost prices one API call's usage at model's row of Prices, on
// its long-prompt card when the call's prompt is over longPromptTokens.
// It reports false for a model the table lacks, whose cost is unknown.
// The usage must be one call's: summed over a turn's calls, it no longer
// says how long each prompt was, and a turn may change model.
func EstimateCost(model string, usage Usage) (float64, bool) {
	price, ok := Prices[model]
	if !ok {
		price, ok = Prices[dateSuffix.ReplaceAllString(model, "")]
	}
	if !ok {
		return 0, false
	}
	prompt := usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
	if price.LongPrompt != nil && prompt > longPromptTokens {
		price = *price.LongPrompt
	}
	hourWrites := 0
	if usage.CacheCreation != nil {
		hourWrites = min(usage.CacheCreation.Ephemeral1hInputTokens, usage.CacheCreationTokens)
	}
	perToken := func(tokens int, dollarsPerMillion float64) float64 {
		return float64(tokens) * dollarsPerMillion / 1e6
	}
	return perToken(usage.InputTokens, price.Input) +
		perToken(usage.OutputTokens, price.Output) +
		perToken(usage.CacheReadTokens, price.CacheRead) +
		perToken(usage.CacheCreationTokens-hourWrites, price.Input*1.25) +
		perToken(hourWrites, price.Input*2), true
}
