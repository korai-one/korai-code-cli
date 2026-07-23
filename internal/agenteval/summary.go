package agenteval

import (
	"fmt"
	"strings"
)

// FormatLiveSummary renders the per-scenario pass-rate table for a live run,
// plus the suite-wide fence-compliance rate. Output is deterministic:
// scenarios appear in first-seen record order (the runner's fixed order), and
// the format is stable for humans and diffs alike.
func FormatLiveSummary(records []LiveRecord) string {
	type agg struct {
		runs, passed, errs      int
		calls, retries, wrapUps int
	}
	stats := map[string]*agg{}
	var order []string
	for _, r := range records {
		a := stats[r.Scenario]
		if a == nil {
			a = &agg{}
			stats[r.Scenario] = a
			order = append(order, r.Scenario)
		}
		a.runs++
		if r.Pass {
			a.passed++
		}
		if r.Err != "" {
			a.errs++
		}
		a.calls += r.ModelCalls
		a.retries += r.FenceRetries
		a.wrapUps += r.WrapUps
	}
	var b strings.Builder
	b.WriteString("korai eval summary\n")
	b.WriteString("==================\n")
	fmt.Fprintf(&b, "%-18s %5s %5s %7s %9s %8s %8s %5s\n",
		"scenario", "runs", "pass", "rate", "avg_calls", "retries", "wrap_ups", "errs")
	totalCalls, totalRetries := 0, 0
	for _, name := range order {
		a := stats[name]
		fmt.Fprintf(&b, "%-18s %5d %5d %6.1f%% %9.1f %8d %8d %5d\n",
			name, a.runs, a.passed, rate(a.passed, a.runs), avg(a.calls, a.runs), a.retries, a.wrapUps, a.errs)
		totalCalls += a.calls
		totalRetries += a.retries
	}
	if totalCalls > 0 {
		fmt.Fprintf(&b, "\nfence compliance: %.1f%% (%d retried of %d model calls)\n",
			100-rate(totalRetries, totalCalls), totalRetries, totalCalls)
	}
	return b.String()
}

func rate(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func avg(sum, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}
