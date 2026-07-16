package cost

import "github.com/Nevaero/korai-code-cli/internal/apiclient"

// price is USD per one million tokens.
type price struct {
	inputPerM  float64
	outputPerM float64
}

// prices holds estimated per-model rates for models the Korai network may route
// to, keyed by the model name reported in usage. These are public list prices
// used only for the cost estimate; the token accounting itself is
// backend-agnostic. A model with no entry is still token-tracked, just without a
// USD figure. Extend or replace as the routed model set changes (e.g. from a
// generated registry).
var prices = map[string]price{
	"claude-opus-4-8":   {inputPerM: 15, outputPerM: 75},
	"claude-sonnet-4-6": {inputPerM: 3, outputPerM: 15},
	"claude-haiku-4-5":  {inputPerM: 1, outputPerM: 5},
}

// estimateUSD returns the estimated cost for usage under model. The bool is
// false when the model has no known price (tokens are still tracked).
func estimateUSD(model string, u apiclient.Usage) (float64, bool) {
	p, ok := prices[model]
	if !ok {
		return 0, false
	}
	const perMillion = 1_000_000.0
	usd := float64(u.InputTokens)/perMillion*p.inputPerM +
		float64(u.OutputTokens)/perMillion*p.outputPerM
	return usd, true
}
