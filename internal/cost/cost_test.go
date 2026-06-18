package cost_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/cost"
)

func TestTrackerTotals(t *testing.T) {
	t.Parallel()
	tr := cost.NewTracker()
	tr.Add("claude-sonnet-4-6", apiclient.Usage{InputTokens: 100, OutputTokens: 50})
	tr.Add("claude-sonnet-4-6", apiclient.Usage{InputTokens: 10, OutputTokens: 5})
	tr.Add("claude-opus-4-8", apiclient.Usage{InputTokens: 1, OutputTokens: 2})

	in, out := tr.Totals()
	if in != 111 || out != 57 {
		t.Errorf("totals = (%d, %d), want (111, 57)", in, out)
	}
}

func TestSummaryEmpty(t *testing.T) {
	t.Parallel()
	if s := cost.NewTracker().Summary(); !strings.Contains(s, "No usage") {
		t.Errorf("empty summary = %q", s)
	}
}

func TestSummaryWithKnownAndUnknownPrice(t *testing.T) {
	t.Parallel()
	tr := cost.NewTracker()
	tr.Add("claude-sonnet-4-6", apiclient.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	tr.Add("some-future-model", apiclient.Usage{InputTokens: 5, OutputTokens: 5})

	s := tr.Summary()
	// sonnet: 1M in * $3 + 1M out * $15 = $18.0000
	if !strings.Contains(s, "$18.0000") {
		t.Errorf("expected sonnet cost in summary:\n%s", s)
	}
	if !strings.Contains(s, "no price") {
		t.Errorf("expected unknown model marked as no price:\n%s", s)
	}
	if !strings.Contains(s, "total") {
		t.Errorf("expected total line:\n%s", s)
	}
}

func TestTrackerConcurrent(t *testing.T) {
	t.Parallel()
	tr := cost.NewTracker()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Add("m", apiclient.Usage{InputTokens: 1, OutputTokens: 1})
		}()
	}
	wg.Wait()
	if in, out := tr.Totals(); in != 100 || out != 100 {
		t.Errorf("totals = (%d, %d), want (100, 100)", in, out)
	}
}
