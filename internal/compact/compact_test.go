package compact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/compact"
)

// fakeClient returns a fixed summary, recording the request it received.
type fakeClient struct {
	summary string
	gotReq  apiclient.Request
}

func (c *fakeClient) Complete(_ context.Context, req apiclient.Request) (<-chan apiclient.Event, error) {
	c.gotReq = req
	ch := make(chan apiclient.Event, 2)
	ch <- apiclient.TextDeltaEvent{Text: c.summary}
	ch <- apiclient.MessageCompleteEvent{StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

func userMsg(text string) apiclient.Message {
	return apiclient.Message{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: text}}}
}

func TestCompactReplacesOlderMessages(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "earlier: did X and Y"}
	msgs := []apiclient.Message{
		userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"), userMsg("5"), userMsg("6"),
	}

	out, err := compact.Compact(context.Background(), client, msgs, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// 1 summary message + 2 recent = 3.
	if len(out) != 3 {
		t.Fatalf("got %d messages, want 3", len(out))
	}
	summary := out[0].Content[0].(apiclient.TextBlock).Text
	if !strings.Contains(summary, "earlier: did X and Y") {
		t.Errorf("summary message = %q", summary)
	}
	// The last two original messages are preserved verbatim.
	if out[1].Content[0].(apiclient.TextBlock).Text != "5" {
		t.Errorf("recent[0] = %q, want 5", out[1].Content[0].(apiclient.TextBlock).Text)
	}
	if out[2].Content[0].(apiclient.TextBlock).Text != "6" {
		t.Errorf("recent[1] = %q, want 6", out[2].Content[0].(apiclient.TextBlock).Text)
	}
}

func TestCompactNoopWhenShort(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "unused"}
	msgs := []apiclient.Message{userMsg("1"), userMsg("2")}

	out, err := compact.Compact(context.Background(), client, msgs, 4)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("short history should be unchanged, got %d", len(out))
	}
	if client.gotReq.System != "" {
		t.Error("model should not be called for a short history")
	}
}

func TestCompactEmptySummaryErrors(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "   "}
	msgs := []apiclient.Message{userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"), userMsg("5")}

	if _, err := compact.Compact(context.Background(), client, msgs, 2); err == nil {
		t.Error("expected error on empty summary")
	}
}

func TestEstimateTokens(t *testing.T) {
	t.Parallel()
	msgs := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "12345678"}}},      // 8 chars
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.ToolResultBlock{Content: "1234"}}}, // 4 chars
	}
	if got := compact.EstimateTokens(msgs); got != 3 { // 12 chars / 4
		t.Errorf("EstimateTokens = %d, want 3", got)
	}
}

func TestEstimateTokensCountsImages(t *testing.T) {
	t.Parallel()
	textOnly := []apiclient.Message{userMsg("12345678")}
	withImage := []apiclient.Message{
		userMsg("12345678"),
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{
			apiclient.ImageBlock{Source: "data:image/png;base64,AAAA"},
			apiclient.ImageBlock{Source: "data:image/png;base64,BBBB"},
		}},
	}
	base := compact.EstimateTokens(textOnly)
	got := compact.EstimateTokens(withImage)
	// Two images at the flat conservative cost of 1500 tokens each; the data
	// URI's byte length must NOT leak into the estimate.
	if want := base + 3000; got != want {
		t.Errorf("EstimateTokens with images = %d, want %d", got, want)
	}
}

func TestEstimateOverheadCountsSystemAndTools(t *testing.T) {
	t.Parallel()
	system := strings.Repeat("s", 400)
	if got := compact.EstimateOverhead(system, nil); got != 100 {
		t.Errorf("EstimateOverhead(system only) = %d, want 100", got)
	}
	tools := []apiclient.ToolDef{{
		Name:        "Bash",
		Description: "runs a command",
		InputSchema: []byte(`{"type":"object","properties":{"command":{"type":"string"}}}`),
	}}
	withTools := compact.EstimateOverhead(system, tools)
	if withTools <= 100 {
		t.Errorf("EstimateOverhead with tools = %d, want > system-only estimate (the fence block is real prompt bytes)", withTools)
	}
}

// noisyToolResult builds a user message holding a tool result with n identical
// lines — highly compressible by the deterministic condenser.
func noisyToolResult(id string, n int) apiclient.Message {
	return apiclient.Message{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{
		apiclient.ToolResultBlock{ToolCallID: id, Content: strings.Repeat("downloading artifact...\n", n)},
	}}
}

func TestCondenseOlderSquashesOnlyStaleToolResults(t *testing.T) {
	t.Parallel()
	msgs := []apiclient.Message{
		noisyToolResult("old", 200),
		userMsg("middle"),
		noisyToolResult("recent", 200),
	}
	out := compact.CondenseOlder(msgs, 1)

	oldResult := out[0].Content[0].(apiclient.ToolResultBlock)
	if len(oldResult.Content) >= len(msgs[0].Content[0].(apiclient.ToolResultBlock).Content) {
		t.Error("stale tool result was not condensed")
	}
	recent := out[2].Content[0].(apiclient.ToolResultBlock)
	if recent.Content != msgs[2].Content[0].(apiclient.ToolResultBlock).Content {
		t.Error("tool result inside the keep-recent window was modified")
	}
	// The input slice and its blocks are untouched (copy-on-write).
	if msgs[0].Content[0].(apiclient.ToolResultBlock).Content == oldResult.Content {
		t.Error("CondenseOlder mutated its input")
	}
}

func TestAutoDeterministicTierSuffices(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "should not be needed"}
	msgs := []apiclient.Message{
		noisyToolResult("a", 400),
		userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"),
	}
	// The condensed history easily fits a generous budget, so the LLM tier
	// must not run.
	out, err := compact.Auto(context.Background(), client, msgs, 4, 5_000)
	if err != nil {
		t.Fatalf("Auto: %v", err)
	}
	if client.gotReq.System != "" {
		t.Error("LLM tier ran although the deterministic tier fit the budget")
	}
	if len(out) != len(msgs) {
		t.Errorf("deterministic tier changed message count: %d → %d", len(msgs), len(out))
	}
	if got := out[0].Content[0].(apiclient.ToolResultBlock).Content; len(got) >= 400*len("downloading artifact...\n") {
		t.Error("stale tool result not condensed on the deterministic tier")
	}
}

func TestAutoFallsThroughToLLM(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "compressed earlier work"}
	msgs := []apiclient.Message{
		userMsg(strings.Repeat("unique text that cannot be condensed ", 50)),
		userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"), userMsg("5"),
	}
	out, err := compact.Auto(context.Background(), client, msgs, 2, 10)
	if err != nil {
		t.Fatalf("Auto: %v", err)
	}
	if client.gotReq.System == "" {
		t.Error("LLM tier did not run although the budget was exceeded")
	}
	if len(out) != 3 { // summary + 2 recent
		t.Errorf("got %d messages, want 3", len(out))
	}
}
