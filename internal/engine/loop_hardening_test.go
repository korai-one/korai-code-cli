package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// scriptClient replays scripted turns keyed by call index and records every
// request it received, so tests can assert on what the engine sent (tools
// stripped, notices injected, …).
type scriptClient struct {
	script func(call int, req apiclient.Request) []apiclient.Event

	mu   sync.Mutex
	reqs []apiclient.Request
}

func (s *scriptClient) Complete(_ context.Context, req apiclient.Request) (<-chan apiclient.Event, error) {
	s.mu.Lock()
	n := len(s.reqs)
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	events := s.script(n, req)
	ch := make(chan apiclient.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (s *scriptClient) requests() []apiclient.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]apiclient.Request, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// countingTool records how many times Execute ran and returns output produced
// by fn (defaults to a fixed string).
type countingTool struct {
	name  string
	runs  *int
	fn    func(n int) string
	mu    *sync.Mutex
	allow bool
}

func newCountingTool(name string, fn func(n int) string) *countingTool {
	runs := 0
	return &countingTool{name: name, runs: &runs, fn: fn, mu: &sync.Mutex{}, allow: true}
}

func (c *countingTool) Runs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.runs
}

func (c *countingTool) Name() string                       { return c.name }
func (c *countingTool) Description(context.Context) string { return "counting tool" }
func (c *countingTool) InputSchema() *jsonschema.Schema    { return tool.Schema[struct{}]() }
func (c *countingTool) ReadOnly() bool                     { return true }
func (c *countingTool) ConcurrencySafe() bool              { return true }
func (c *countingTool) CheckPermission(context.Context, json.RawMessage, perm.Mode) perm.Decision {
	return perm.DecisionAllow
}
func (c *countingTool) Execute(context.Context, json.RawMessage, tool.Deps) (tool.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.runs++
	out := "ok"
	if c.fn != nil {
		out = c.fn(*c.runs)
	}
	return tool.Result{Content: out}, nil
}

func bypassEngine(t *testing.T, client apiclient.Client, registry *tool.Registry, opts ...engine.Option) *engine.Engine {
	t.Helper()
	permEngine := perm.NewEngine(perm.NewModeSelector(perm.ModeBypassPermissions), perm.Rules{}, perm.DenyAsker{})
	return engine.New(client, registry, permEngine, tool.Deps{}, opts...)
}

func userTurn(text string) []apiclient.Message {
	return []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: text}}},
	}
}

func toolCallTurn(id, name string, input json.RawMessage) []apiclient.Event {
	return []apiclient.Event{
		apiclient.ToolCallStartEvent{ID: id, Name: name},
		apiclient.ToolCallCompleteEvent{ID: id, Name: name, Input: input},
		apiclient.MessageCompleteEvent{StopReason: apiclient.StopToolUse},
	}
}

func textTurn(text string) []apiclient.Event {
	return []apiclient.Event{
		apiclient.TextDeltaEvent{Text: text},
		apiclient.MessageCompleteEvent{StopReason: apiclient.StopEndTurn},
	}
}

// lastUserText returns the concatenated text blocks of the last user message
// in a request's history.
func lastUserText(req apiclient.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != apiclient.RoleUser {
			continue
		}
		var b strings.Builder
		for _, blk := range req.Messages[i].Content {
			if txt, ok := blk.(apiclient.TextBlock); ok {
				b.WriteString(txt.Text)
			}
		}
		return b.String()
	}
	return ""
}

// TestMaxToolTurnsForcesWrapUp verifies the turn budget: after the configured
// number of tool iterations the engine injects the wrap-up instruction, strips
// tools from the final request, and records the model's summary — it never
// hard-aborts.
func TestMaxToolTurnsForcesWrapUp(t *testing.T) {
	t.Parallel()

	probe := newCountingTool("Probe", func(n int) string { return fmt.Sprintf("probe result %c", 'a'+n) })
	registry := tool.NewRegistry()
	registry.Register(probe)

	client := &scriptClient{script: func(call int, req apiclient.Request) []apiclient.Event {
		if call < 3 {
			// Distinct args per call so the loop detector stays quiet and the
			// budget is what ends the run.
			input, _ := json.Marshal(map[string]int{"n": call})
			return toolCallTurn(fmt.Sprintf("c%d", call), "Probe", input)
		}
		return textTurn("wrap-up summary")
	}}

	eng := bypassEngine(t, client, registry, engine.WithMaxToolTurns(3))

	var history []apiclient.Message
	var finalText strings.Builder
	for evt := range eng.Run(context.Background(), userTurn("dig in"), "sys") {
		switch v := evt.(type) {
		case engine.TextEvent:
			finalText.WriteString(v.Text)
		case engine.DoneEvent:
			history = v.Messages
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	reqs := client.requests()
	if len(reqs) != 4 {
		t.Fatalf("requests = %d, want 4 (3 tool turns + 1 wrap-up)", len(reqs))
	}
	if probe.Runs() != 3 {
		t.Errorf("tool ran %d times, want 3", probe.Runs())
	}
	last := reqs[3]
	if len(last.Tools) != 0 {
		t.Errorf("wrap-up request still carries %d tools, want none", len(last.Tools))
	}
	if got := lastUserText(last); !strings.Contains(got, "maximum number of tool iterations") {
		t.Errorf("wrap-up request missing the wrap-up notice; last user text: %q", got)
	}
	if finalText.String() != "wrap-up summary" {
		t.Errorf("final text = %q, want the wrap-up summary", finalText.String())
	}
	if len(history) == 0 {
		t.Fatal("no DoneEvent history")
	}
	lastMsg := history[len(history)-1]
	if lastMsg.Role != apiclient.RoleAssistant {
		t.Errorf("history must end with the assistant summary, got role %q", lastMsg.Role)
	}
}

// TestLoopDetectorEndToEnd drives the full escalation through the engine:
// identical call → silent, warn, veto, veto → forced wrap-up. The emitted event
// stream is compared against a golden transcript.
func TestLoopDetectorEndToEnd(t *testing.T) {
	t.Parallel()

	same := newCountingTool("Same", func(int) string { return "state: unchanged" })
	registry := tool.NewRegistry()
	registry.Register(same)

	client := &scriptClient{script: func(call int, req apiclient.Request) []apiclient.Event {
		if call < 4 {
			return toolCallTurn(fmt.Sprintf("c%d", call), "Same", json.RawMessage(`{"q":"status"}`))
		}
		return textTurn("giving my final answer without more tool calls")
	}}

	eng := bypassEngine(t, client, registry)

	var transcript strings.Builder
	for evt := range eng.Run(context.Background(), userTurn("check the status"), "") {
		switch v := evt.(type) {
		case engine.TextEvent:
			fmt.Fprintf(&transcript, "TEXT %q\n", v.Text)
		case engine.ToolStartEvent:
			fmt.Fprintf(&transcript, "TOOL_START %s %s\n", v.Name, v.Input)
		case engine.ToolResultEvent:
			fmt.Fprintf(&transcript, "TOOL_RESULT %s error=%v %q\n", v.Name, v.Result.IsError, v.Result.Content)
		case engine.DoneEvent:
			transcript.WriteString("DONE\n")
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	goldenPath := filepath.Join("..", "..", "testdata", "golden", "loop_hardening.txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(transcript.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing — run with -update to create it: %v", err)
	}
	if diff := cmp.Diff(string(wantBytes), transcript.String()); diff != "" {
		t.Errorf("transcript mismatch (-want +got):\n%s", diff)
	}

	// The tool ran exactly twice: the 3rd and 4th identical calls were vetoed.
	if same.Runs() != 2 {
		t.Errorf("tool ran %d times, want 2", same.Runs())
	}
	// Two vetoes forced the wrap-up: the final request carries no tools and the
	// loop wrap-up notice.
	reqs := client.requests()
	if len(reqs) != 5 {
		t.Fatalf("requests = %d, want 5", len(reqs))
	}
	if len(reqs[4].Tools) != 0 {
		t.Errorf("wrap-up request still carries tools")
	}
	if got := lastUserText(reqs[4]); !strings.Contains(got, "stuck repeating tool calls") {
		t.Errorf("wrap-up request missing the loop notice; last user text: %q", got)
	}
}

// TestMalformedFenceRetryUnterminated verifies the one-shot retry when the
// model leaves an unterminated fence in its reply: the engine appends a
// corrective notice quoting the fragment and re-runs the turn instead of
// finishing silently.
func TestMalformedFenceRetryUnterminated(t *testing.T) {
	t.Parallel()

	registry := tool.NewRegistry()

	malformed := `Let me read it. <tool:ReadFile>{"path": "a.go"`
	client := &scriptClient{script: func(call int, req apiclient.Request) []apiclient.Event {
		if call == 0 {
			return textTurn(malformed)
		}
		return textTurn("recovered final answer")
	}}

	eng := bypassEngine(t, client, registry)

	var history []apiclient.Message
	for evt := range eng.Run(context.Background(), userTurn("read a.go"), "") {
		switch v := evt.(type) {
		case engine.DoneEvent:
			history = v.Messages
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	reqs := client.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (original + retry)", len(reqs))
	}
	notice := lastUserText(reqs[1])
	if !strings.Contains(notice, "malformed tool call") {
		t.Errorf("retry request missing the corrective notice: %q", notice)
	}
	if !strings.Contains(notice, `<tool:ReadFile>{"path": "a.go"`) {
		t.Errorf("corrective notice does not quote the malformed fence: %q", notice)
	}
	// History: user, assistant (malformed, flattened), user (notice), assistant (final).
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4", len(history))
	}
	if history[len(history)-1].Role != apiclient.RoleAssistant {
		t.Error("history must end with the recovered assistant answer")
	}
}

// TestMalformedFenceRetryInvalidJSON verifies that a tool call whose body is
// not a valid JSON object triggers the retry without ever reaching Execute,
// and that the corrected call on the retry turn runs normally.
func TestMalformedFenceRetryInvalidJSON(t *testing.T) {
	t.Parallel()

	probe := newCountingTool("Probe", nil)
	registry := tool.NewRegistry()
	registry.Register(probe)

	client := &scriptClient{script: func(call int, req apiclient.Request) []apiclient.Event {
		switch call {
		case 0:
			return toolCallTurn("c0", "Probe", json.RawMessage(`{"path": oops}`))
		case 1:
			return toolCallTurn("c1", "Probe", json.RawMessage(`{"path":"a.go"}`))
		default:
			return textTurn("done")
		}
	}}

	eng := bypassEngine(t, client, registry)
	for evt := range eng.Run(context.Background(), userTurn("go"), "") {
		if v, ok := evt.(engine.ErrorEvent); ok {
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	if probe.Runs() != 1 {
		t.Errorf("tool ran %d times, want 1 (malformed call must not execute)", probe.Runs())
	}
	reqs := client.requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	if notice := lastUserText(reqs[1]); !strings.Contains(notice, "malformed tool call") {
		t.Errorf("retry request missing the corrective notice: %q", notice)
	}
	// The retry turn — and only the retry turn — opts into grammar-enforced
	// tool fences.
	if reqs[0].ConstrainTools {
		t.Error("first request must not be constrained")
	}
	if !reqs[1].ConstrainTools {
		t.Error("malformed-fence retry request must set ConstrainTools")
	}
	if reqs[2].ConstrainTools {
		t.Error("post-retry request must not be constrained")
	}
}

// TestSamplingDefaultsStamped verifies WithSamplingDefaults puts the
// configured sampling on every request the engine builds.
func TestSamplingDefaultsStamped(t *testing.T) {
	t.Parallel()

	client := &scriptClient{script: func(int, apiclient.Request) []apiclient.Event {
		return textTurn("ok")
	}}
	seed := 7
	temp := 0.0
	eng := bypassEngine(t, client, tool.NewRegistry(),
		engine.WithSamplingDefaults(apiclient.Sampling{Seed: &seed, Temperature: &temp}))

	for evt := range eng.Run(context.Background(), userTurn("hi"), "") {
		if v, ok := evt.(engine.ErrorEvent); ok {
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	reqs := client.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	s := reqs[0].Sampling
	if s.Seed == nil || *s.Seed != 7 {
		t.Errorf("seed = %v, want 7", s.Seed)
	}
	if s.Temperature == nil || *s.Temperature != 0.0 {
		t.Errorf("temperature = %v, want explicit 0", s.Temperature)
	}
}

// TestMalformedFenceSecondFailureDegrades verifies the retry is one-shot: a
// second malformed turn goes through normal dispatch, where input
// pre-validation blocks it with an error tool result (Execute is never
// reached) and the loop continues.
func TestMalformedFenceSecondFailureDegrades(t *testing.T) {
	t.Parallel()

	probe := newCountingTool("Probe", nil)
	registry := tool.NewRegistry()
	registry.Register(probe)

	bad := json.RawMessage(`{"path": oops}`)
	client := &scriptClient{script: func(call int, req apiclient.Request) []apiclient.Event {
		switch call {
		case 0, 1:
			return toolCallTurn(fmt.Sprintf("c%d", call), "Probe", bad)
		default:
			return textTurn("done without tools")
		}
	}}

	eng := bypassEngine(t, client, registry)

	var badResult *engine.ToolResultEvent
	for evt := range eng.Run(context.Background(), userTurn("go"), "") {
		switch v := evt.(type) {
		case engine.ToolResultEvent:
			r := v
			badResult = &r
		case engine.ErrorEvent:
			t.Fatalf("engine error: %v", v.Err)
		}
	}

	if probe.Runs() != 0 {
		t.Errorf("tool ran %d times, want 0 (garbage input must never reach Execute)", probe.Runs())
	}
	if badResult == nil {
		t.Fatal("expected a ToolResultEvent for the degraded malformed call")
	}
	if !badResult.Result.IsError || !strings.Contains(badResult.Result.Content, "not a valid JSON object") {
		t.Errorf("degraded call result = %+v, want a pre-validation error", badResult.Result)
	}
	if reqs := client.requests(); len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
}
