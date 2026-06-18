package cost

import "github.com/Nevaero/korai-code-cli/internal/apiclient"

// price is USD per one million tokens.
type price struct {
	inputPerM  float64
	outputPerM float64
}

// prices holds estimated per-model rates.
//
// TODO KORAI SDK: these are Anthropic list prices and are the only
// backend-specific part of this package. When the Korai inference SDK is wired
// in, pricing should come from it (or be removed if the network is free), while
// the token accounting above stays unchanged.
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
