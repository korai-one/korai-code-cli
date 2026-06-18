package engine_test

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/readfile"
)

var update = flag.Bool("update", false, "update golden files")

// mockClient replays a fixed sequence of apiclient.Events.
type mockClient struct {
	turns [][]apiclient.Event
	call  int
}

func (m *mockClient) Complete(_ context.Context, _ apiclient.Request) (<-chan apiclient.Event, error) {
	events := m.turns[m.call]
	m.call++
	ch := make(chan apiclient.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// TestEngineReadFileLoop verifies the full tool-calling loop:
//  1. Model responds with a ReadFile tool call.
//  2. Engine executes ReadFile against testdata/fixtures/hello.txt.
//  3. Model receives the result and produces a final text response.
//  4. Collected output matches the golden file.
func TestEngineReadFileLoop(t *testing.T) {
	t.Parallel()

	fixtureDir, err := filepath.Abs("../../testdata/fixtures")
	if err != nil {
		t.Fatal(err)
	}

	inputJSON, _ := json.Marshal(map[string]string{"path": "hello.txt"})

	client := &mockClient{
		turns: [][]apiclient.Event{
			// Turn 1: model calls ReadFile.
			{
				apiclient.ToolCallStartEvent{ID: "call_1", Name: "ReadFile"},
				apiclient.ToolCallCompleteEvent{
					ID:    "call_1",
					Name:  "ReadFile",
					Input: inputJSON,
				},
				apiclient.MessageCompleteEvent{StopReason: "tool_use"},
			},
			// Turn 2: model produces final answer after seeing the file contents.
			{
				apiclient.TextDeltaEvent{Text: "The file says: hello from korai\n"},
				apiclient.MessageCompleteEvent{StopReason: "end_turn"},
			},
		},
	}

	registry := tool.NewRegistry()
	registry.Register(readfile.New())

	permEngine := perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{})
	eng := engine.New(client, registry, permEngine, tool.Deps{WorkDir: fixtureDir})
	messages := []apiclient.Message{
		{
			Role:    apiclient.RoleUser,
			Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "What is in hello.txt?"}},
		},
	}

	var got strings.Builder
	for evt := range eng.Run(context.Background(), messages, "") {
		switch v := evt.(type) {
		case engine.TextEvent:
			got.WriteString(v.Text)
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	goldenPath := filepath.Join("..", "..", "testdata", "golden", "readfile_loop.txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing — run with -update to create it: %v", err)
	}
	want := string(wantBytes)

	if diff := cmp.Diff(want, got.String()); diff != "" {
		t.Errorf("output mismatch (-want +got):\n%s", diff)
	}
}

// TestPreToolUseHookBlocks verifies that a PreToolUse hook returning block=true
// prevents tool execution and surfaces the reason as an error result.
func TestPreToolUseHookBlocks(t *testing.T) {
	t.Parallel()

	inputJSON, _ := json.Marshal(map[string]string{"path": "hello.txt"})
	client := &mockClient{turns: [][]apiclient.Event{
		{
			apiclient.ToolCallCompleteEvent{ID: "c1", Name: "ReadFile", Input: inputJSON},
			apiclient.MessageCompleteEvent{StopReason: "tool_use"},
		},
		{
			apiclient.TextDeltaEvent{Text: "ok"},
			apiclient.MessageCompleteEvent{StopReason: "end_turn"},
		},
	}}

	registry := tool.NewRegistry()
	registry.Register(readfile.New())
	permEngine := perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{})

	var fired []string
	hook := func(_ context.Context, event, _ string, _ json.RawMessage) (bool, string) {
		fired = append(fired, event)
		if event == engine.HookPreToolUse {
			return true, "blocked by policy"
		}
		return false, ""
	}

	eng := engine.New(client, registry, permEngine, tool.Deps{WorkDir: t.TempDir()}, engine.WithHooks(hook))

	var toolResult engine.ToolResultEvent
	var sawStart bool
	for evt := range eng.Run(context.Background(), []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "read it"}}},
	}, "") {
		switch v := evt.(type) {
		case engine.ToolStartEvent:
			sawStart = true
		case engine.ToolResultEvent:
			toolResult = v
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	if sawStart {
		t.Error("tool should not start when a PreToolUse hook blocks it")
	}
	if !toolResult.Result.IsError || toolResult.Result.Content != "blocked by policy" {
		t.Errorf("result = %+v, want blocked error with reason", toolResult.Result)
	}
	if len(fired) == 0 || fired[0] != engine.HookSessionStart {
		t.Errorf("expected SessionStart to fire first, got %v", fired)
	}
}

// TestAutoCompactFires verifies the engine compacts the history before a turn
// when the estimate exceeds the threshold, emitting a CompactedEvent.
func TestAutoCompactFires(t *testing.T) {
	t.Parallel()

	client := &mockClient{turns: [][]apiclient.Event{
		{
			apiclient.TextDeltaEvent{Text: "done"},
			apiclient.MessageCompleteEvent{StopReason: "end_turn"},
		},
	}}
	registry := tool.NewRegistry()
	permEngine := perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{})

	var compactCalled bool
	compactFn := func(_ context.Context, msgs []apiclient.Message) ([]apiclient.Message, error) {
		compactCalled = true
		return msgs[len(msgs)-1:], nil // keep only the last message
	}
	estimate := func([]apiclient.Message) int { return 1000 } // always over threshold

	eng := engine.New(client, registry, permEngine, tool.Deps{},
		engine.WithAutoCompact(100, estimate, compactFn))

	in := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "a"}}},
		{Role: apiclient.RoleAssistant, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "b"}}},
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "c"}}},
	}

	var compacted *engine.CompactedEvent
	for evt := range eng.Run(context.Background(), in, "") {
		if c, ok := evt.(engine.CompactedEvent); ok {
			compacted = &c
		}
	}
	if !compactCalled {
		t.Error("compact function should have been called")
	}
	if compacted == nil || compacted.Before != 3 || compacted.After != 1 {
		t.Errorf("CompactedEvent = %+v, want before=3 after=1", compacted)
	}
}

// TestAutoCompactSkippedUnderThreshold verifies no compaction below the threshold.
func TestAutoCompactSkippedUnderThreshold(t *testing.T) {
	t.Parallel()

	client := &mockClient{turns: [][]apiclient.Event{
		{apiclient.TextDeltaEvent{Text: "ok"}, apiclient.MessageCompleteEvent{StopReason: "end_turn"}},
	}}
	eng := engine.New(client, tool.NewRegistry(),
		perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{}),
		tool.Deps{},
		engine.WithAutoCompact(100, func([]apiclient.Message) int { return 10 },
			func(_ context.Context, m []apiclient.Message) ([]apiclient.Message, error) {
				t.Error("compact must not run under threshold")
				return m, nil
			}))

	events := eng.Run(context.Background(), []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hi"}}},
	}, "")
	for evt := range events {
		if _, ok := evt.(engine.CompactedEvent); ok {
			t.Error("no CompactedEvent expected under threshold")
		}
	}
}
