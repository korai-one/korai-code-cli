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

// TestSystemSectionPerTurnVisibility verifies the dynamic system-section seam:
// the section function is re-evaluated on every model call within a single
// Run, so state recorded mid-turn (e.g. a Remember write during the tool loop)
// appears on the very next request — and it is appended at the END of the
// system prompt, after the suffix, keeping the stable prefix intact.
func TestSystemSectionPerTurnVisibility(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	section := "# Memory\n\n- initial note"

	// The tool mutates the section mid-turn, like a Remember write would.
	mutator := newCountingTool("Mutate", func(int) string {
		mu.Lock()
		section = "# Memory\n\n- note added mid-turn"
		mu.Unlock()
		return "done"
	})

	client := &scriptClient{script: func(call int, _ apiclient.Request) []apiclient.Event {
		if call == 0 {
			return toolCallTurn("c1", "Mutate", json.RawMessage(`{}`))
		}
		return textTurn("finished")
	}}

	registry := tool.NewRegistry()
	registry.Register(mutator)

	var seenUser []string
	eng := bypassEngine(t, client, registry,
		engine.WithSystemSuffix(func() string { return "SUFFIX" }),
		engine.WithSystemSection(func(latestUser string) string {
			seenUser = append(seenUser, latestUser)
			mu.Lock()
			defer mu.Unlock()
			return section
		}),
	)

	for evt := range eng.Run(context.Background(), userTurn("please mutate"), "BASE SYSTEM") {
		if e, ok := evt.(engine.ErrorEvent); ok {
			t.Fatalf("engine error: %v", e.Err)
		}
	}

	reqs := client.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	if !strings.Contains(reqs[0].System, "initial note") {
		t.Errorf("request 1 system missing initial section:\n%s", reqs[0].System)
	}
	if !strings.Contains(reqs[1].System, "note added mid-turn") {
		t.Errorf("request 2 system missing mid-turn write — the section is not per-turn:\n%s", reqs[1].System)
	}
	// Ordering: base prefix stays first, suffix next, dynamic section last.
	for i, req := range reqs {
		base := strings.Index(req.System, "BASE SYSTEM")
		suffix := strings.Index(req.System, "SUFFIX")
		mem := strings.Index(req.System, "# Memory")
		if base != 0 || suffix < base || mem < suffix {
			t.Errorf("request %d system order wrong (base=%d suffix=%d mem=%d):\n%s",
				i+1, base, suffix, mem, req.System)
		}
	}
	// The section provider received the latest genuine user text, not the
	// tool-result payloads.
	for i, u := range seenUser {
		if u != "please mutate" {
			t.Errorf("section call %d saw latest user %q, want %q", i, u, "please mutate")
		}
	}
}
