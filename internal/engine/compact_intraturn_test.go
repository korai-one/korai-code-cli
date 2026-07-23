package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// recordingCompactor is a CompactFunc that records every input it received and
// replaces it with a single summary message.
type recordingCompactor struct {
	mu    sync.Mutex
	calls [][]apiclient.Message
}

func (r *recordingCompactor) fn(_ context.Context, msgs []apiclient.Message) ([]apiclient.Message, error) {
	r.mu.Lock()
	cp := make([]apiclient.Message, len(msgs))
	copy(cp, msgs)
	r.calls = append(r.calls, cp)
	r.mu.Unlock()
	return []apiclient.Message{{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "SUMMARY"}},
	}}, nil
}

func (r *recordingCompactor) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// estimateByCount is a token estimator that charges a fixed price per message,
// making thresholds trivially scriptable.
func estimateByCount(per int) func([]apiclient.Message) int {
	return func(msgs []apiclient.Message) int { return per * len(msgs) }
}

// TestIntraTurnCompactionPreservesCurrentRun drives a run whose tool loop
// crosses the threshold mid-turn and verifies that (a) compaction fires
// between iterations, (b) only pre-run history is summarized, and (c) the
// current run's messages survive verbatim in the final history.
func TestIntraTurnCompactionPreservesCurrentRun(t *testing.T) {
	t.Parallel()

	echo := newCountingTool("Echo", func(int) string { return "tool output" })
	registry := tool.NewRegistry()
	registry.Register(echo)

	client := &scriptClient{script: func(call int, _ apiclient.Request) []apiclient.Event {
		if call < 2 {
			return toolCallTurn("c"+string(rune('1'+call)), "Echo", json.RawMessage(`{}`))
		}
		return textTurn("done")
	}}

	compactor := &recordingCompactor{}
	// 7 messages at Run start (6 old + the prompt) at 10 each = 70 ≤ threshold
	// 100, so the pre-run check stays quiet. Each tool iteration adds 2
	// messages (+20): 90 after the first, 110 entering the third loop pass —
	// the intra-turn check must fire there and may summarize only the 6 old
	// messages (the prompt belongs to the current run).
	preRun := make([]apiclient.Message, 0, 6)
	for i := 0; i < 6; i++ {
		preRun = append(preRun, userTurn("old message")[0])
	}
	messages := append(preRun, userTurn("current prompt")[0])

	eng := bypassEngine(t, client, registry,
		engine.WithAutoCompact(100, estimateByCount(10), compactor.fn))

	var final []apiclient.Message
	var compacted []engine.CompactedEvent
	for evt := range eng.Run(context.Background(), messages, "sys") {
		switch v := evt.(type) {
		case engine.CompactedEvent:
			compacted = append(compacted, v)
		case engine.DoneEvent:
			final = v.Messages
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	if compactor.callCount() == 0 {
		t.Fatal("compaction never fired mid-turn")
	}
	if len(compacted) == 0 {
		t.Fatal("no CompactedEvent emitted")
	}
	// Only pre-run history goes to the compactor: never the current prompt or
	// the turn's own tool traffic.
	for i, call := range compactor.calls {
		for _, m := range call {
			for _, b := range m.Content {
				if tb, ok := b.(apiclient.TextBlock); ok {
					if tb.Text == "current prompt" || strings.Contains(tb.Text, "tool output") {
						t.Errorf("compactor call %d received current-run message %q", i, tb.Text)
					}
				}
			}
		}
	}
	// The current run's messages survive verbatim in the final history.
	var texts []string
	for _, m := range final {
		for _, b := range m.Content {
			switch v := b.(type) {
			case apiclient.TextBlock:
				texts = append(texts, v.Text)
			case apiclient.ToolResultBlock:
				texts = append(texts, v.Content)
			}
		}
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"current prompt", "SUMMARY", "tool output", "done"} {
		if !strings.Contains(joined, want) {
			t.Errorf("final history missing %q:\n%s", want, joined)
		}
	}
}

// TestIntraTurnCompactionSkipsWhenCurrentRunTooBig verifies the guard: when
// the current run's own messages exceed the budget there is nothing safe to
// compact, so the compactor must NOT be called mid-turn.
func TestIntraTurnCompactionSkipsWhenCurrentRunTooBig(t *testing.T) {
	t.Parallel()

	echo := newCountingTool("Echo", nil)
	registry := tool.NewRegistry()
	registry.Register(echo)

	client := &scriptClient{script: func(call int, _ apiclient.Request) []apiclient.Event {
		if call == 0 {
			return toolCallTurn("c1", "Echo", json.RawMessage(`{}`))
		}
		return textTurn("done")
	}}

	compactor := &recordingCompactor{}
	// History: one old message + the prompt, at 40 tokens per message. Pre-run
	// check: 2×40 = 80 ≤ 100. After the first tool iteration the history is 4
	// messages (160 > 100), but the current run (prompt + assistant + results)
	// alone is 3 messages = 120 > 100 → nothing safe to compact, skip.
	eng := bypassEngine(t, client, registry,
		engine.WithAutoCompact(100, estimateByCount(40), compactor.fn))

	messages := append(userTurn("old"), userTurn("prompt")...)
	for evt := range eng.Run(context.Background(), messages, "sys") {
		if v, ok := evt.(engine.ErrorEvent); ok {
			t.Fatalf("engine error: %v", v.Err)
		}
	}
	if n := compactor.callCount(); n != 0 {
		t.Errorf("compactor ran %d times, want 0 (current run alone exceeds budget)", n)
	}
}

// TestOverheadEstimatorTriggersCompaction verifies the honest estimate: a
// history that fits the threshold on its own must still compact when the
// system prompt + tool schema overhead pushes the total over it.
func TestOverheadEstimatorTriggersCompaction(t *testing.T) {
	t.Parallel()

	registry := tool.NewRegistry()
	client := &scriptClient{script: func(int, apiclient.Request) []apiclient.Event {
		return textTurn("done")
	}}

	run := func(overhead engine.OverheadEstimator) int {
		compactor := &recordingCompactor{}
		opts := []engine.Option{engine.WithAutoCompact(100, estimateByCount(10), compactor.fn)}
		if overhead != nil {
			opts = append(opts, engine.WithOverheadEstimator(overhead))
		}
		eng := bypassEngine(t, client, registry, opts...)
		msgs := make([]apiclient.Message, 0, 6)
		for i := 0; i < 6; i++ {
			msgs = append(msgs, userTurn("m")[0])
		}
		for evt := range eng.Run(context.Background(), msgs, "sys") {
			if v, ok := evt.(engine.ErrorEvent); ok {
				t.Fatalf("engine error: %v", v.Err)
			}
		}
		return compactor.callCount()
	}

	// 6 messages × 10 = 60 ≤ 100: no compaction without overhead accounting…
	if n := run(nil); n != 0 {
		t.Errorf("compaction without overhead ran %d times, want 0", n)
	}
	// …but with 50 tokens of system/tool overhead the honest total is 110.
	var sawSystem string
	n := run(func(system string, _ []apiclient.ToolDef) int {
		sawSystem = system
		return 50
	})
	if n == 0 {
		t.Error("compaction with overhead accounting never ran")
	}
	if !strings.Contains(sawSystem, "sys") {
		t.Errorf("overhead estimator saw system %q, want the composed system prompt", sawSystem)
	}
}

// TestDynamicThresholdOverridesStatic verifies WithCompactThreshold: the
// dynamic value wins over WithAutoCompact's static one.
func TestDynamicThresholdOverridesStatic(t *testing.T) {
	t.Parallel()

	registry := tool.NewRegistry()
	client := &scriptClient{script: func(int, apiclient.Request) []apiclient.Event {
		return textTurn("done")
	}}
	compactor := &recordingCompactor{}
	// Static threshold 1000 would never trigger on 60 estimated tokens; the
	// dynamic threshold of 50 must.
	eng := bypassEngine(t, client, registry,
		engine.WithAutoCompact(1000, estimateByCount(10), compactor.fn),
		engine.WithCompactThreshold(func() int { return 50 }))

	msgs := make([]apiclient.Message, 0, 6)
	for i := 0; i < 6; i++ {
		msgs = append(msgs, userTurn("m")[0])
	}
	for evt := range eng.Run(context.Background(), msgs, "sys") {
		if v, ok := evt.(engine.ErrorEvent); ok {
			t.Fatalf("engine error: %v", v.Err)
		}
	}
	if compactor.callCount() == 0 {
		t.Error("dynamic threshold did not trigger compaction")
	}
}
