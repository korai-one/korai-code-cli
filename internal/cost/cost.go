// Package cost tracks cumulative token usage and estimates spend. It consumes
// only apiclient.Usage (the anti-corruption type), so it is independent of the
// inference backend. Only the USD price table is backend-specific — see prices.go.
package cost

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// Tracker accumulates token usage per model. It is safe for concurrent use: the
// engine records usage from its goroutine while the /cost command reads it.
type Tracker struct {
	mu    sync.Mutex
	byMod map[string]apiclient.Usage
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{byMod: make(map[string]apiclient.Usage)}
}

// Add records one model call's usage. An empty model is bucketed as "unknown".
func (t *Tracker) Add(model string, u apiclient.Usage) {
	if model == "" {
		model = "unknown"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	agg := t.byMod[model]
	agg.InputTokens += u.InputTokens
	agg.OutputTokens += u.OutputTokens
	t.byMod[model] = agg
}

// Totals returns the summed input and output tokens across all models.
func (t *Tracker) Totals() (input, output int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, u := range t.byMod {
		input += u.InputTokens
		output += u.OutputTokens
	}
	return input, output
}

// Summary renders a human-readable usage and estimated-cost report.
func (t *Tracker) Summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.byMod) == 0 {
		return "No usage recorded yet."
	}

	models := make([]string, 0, len(t.byMod))
	for m := range t.byMod {
		models = append(models, m)
	}
	sort.Strings(models)

	var (
		totalIn, totalOut int64
		totalUSD          float64
		b                 = &strBuilder{}
	)
	b.line("Token usage:")
	for _, m := range models {
		u := t.byMod[m]
		usd, known := estimateUSD(m, u)
		totalIn += u.InputTokens
		totalOut += u.OutputTokens
		totalUSD += usd
		if known {
			b.linef("  %-22s in=%-8d out=%-8d  ~$%.4f", m, u.InputTokens, u.OutputTokens, usd)
		} else {
			b.linef("  %-22s in=%-8d out=%-8d  (no price)", m, u.InputTokens, u.OutputTokens)
		}
	}
	b.linef("  %-22s in=%-8d out=%-8d  ~$%.4f", "total", totalIn, totalOut, totalUSD)
	b.line("Cost is an estimate; pricing will come from the Korai backend.")
	return b.String()
}

// strBuilder is a tiny newline-joining helper to keep Summary readable.
type strBuilder struct {
	parts []string
}

func (s *strBuilder) line(v string)            { s.parts = append(s.parts, v) }
func (s *strBuilder) linef(f string, a ...any) { s.parts = append(s.parts, fmt.Sprintf(f, a...)) }
func (s *strBuilder) String() string {
	out := ""
	for i, p := range s.parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}
