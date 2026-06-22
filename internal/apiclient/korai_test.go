package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestConvertToKoraiMessages verifies that block-structured messages flatten
// into Korai's OpenAI-style list: user text, an assistant turn carrying tool
// calls, and a role="tool" result whose Name is recovered from the matching
// assistant tool call.
func TestConvertToKoraiMessages(t *testing.T) {
	t.Parallel()

	msgs := []Message{
		{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "read x"}}},
		{Role: RoleAssistant, Content: []ContentBlock{
			TextBlock{Text: "ok"},
			ToolCallBlock{ID: "c1", Name: "ReadFile", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "c1", Content: "data"},
		}},
	}

	got, err := convertToKoraiMessages(msgs)
	if err != nil {
		t.Fatalf("convertToKoraiMessages: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "read x" {
		t.Errorf("msg[0] = %+v, want user/read x", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "ok" {
		t.Errorf("msg[1] = %+v, want assistant/ok", got[1])
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "c1" || got[1].ToolCalls[0].Name != "ReadFile" {
		t.Errorf("msg[1] tool calls = %+v, want one ReadFile call", got[1].ToolCalls)
	}
	if got[1].ToolCalls[0].Input["path"] != "x" {
		t.Errorf("tool call input = %+v, want path=x", got[1].ToolCalls[0].Input)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "c1" || got[2].Content != "data" {
		t.Errorf("msg[2] = %+v, want tool result for c1", got[2])
	}
	if got[2].Name != "ReadFile" {
		t.Errorf("msg[2] name = %q, want ReadFile (recovered from the call)", got[2].Name)
	}
}

// TestConvertToKoraiMessagesErrorResult verifies an error tool result is marked
// in the content the model sees.
func TestConvertToKoraiMessagesErrorResult(t *testing.T) {
	t.Parallel()

	got, err := convertToKoraiMessages([]Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			ToolCallBlock{ID: "c1", Name: "Bash", Input: json.RawMessage(`{}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "c1", Content: "boom", IsError: true},
		}},
	})
	if err != nil {
		t.Fatalf("convertToKoraiMessages: %v", err)
	}
	// assistant + tool result, no stray empty user message.
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(got), got)
	}
	if got[1].Content != "ERROR: boom" {
		t.Errorf("error result content = %q, want \"ERROR: boom\"", got[1].Content)
	}
}

// TestConvertToKoraiTools verifies tool defs render into the OpenAI function
// shape with the JSON Schema passed through as parameters.
func TestConvertToKoraiTools(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	got, err := convertToKoraiTools([]ToolDef{{Name: "ReadFile", Description: "reads a file", InputSchema: schema}})
	if err != nil {
		t.Fatalf("convertToKoraiTools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}
	// Round-trip through JSON to assert the wire shape independent of the SDK type.
	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var shape struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if shape.Type != "function" || shape.Function.Name != "ReadFile" {
		t.Errorf("shape = %+v, want function/ReadFile", shape)
	}
	if shape.Function.Parameters["type"] != "object" {
		t.Errorf("parameters not passed through: %+v", shape.Function.Parameters)
	}
}

func TestConvertToKoraiToolsBadSchema(t *testing.T) {
	t.Parallel()
	_, err := convertToKoraiTools([]ToolDef{{Name: "Broken", InputSchema: json.RawMessage(`{not json`)}})
	if err == nil {
		t.Fatal("expected error for invalid schema, got nil")
	}
}

// drainEvents collects all events from a Complete channel.
func drainEvents(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

// TestKoraiCompleteText drives Complete against a stub server returning a plain
// text completion, and asserts the synthesized event sequence and usage.
func TestKoraiCompleteText(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Stream {
			t.Errorf("buffered path must not request streaming")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"x","object":"chat.completion","model":"auto",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}
		}`)
	}))
	defer srv.Close()

	c := NewKoraiClient("sk-test", srv.URL, "auto")
	ch, err := c.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := drainEvents(t, ch)
	want := []Event{
		TextDeltaEvent{Text: "hello there"},
		MessageCompleteEvent{StopReason: "stop", Usage: Usage{InputTokens: 12, OutputTokens: 3}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("events mismatch (-want +got):\n%s", diff)
	}
}

// TestKoraiCompleteToolCall verifies that a tool call in the buffered response
// is replayed as a start+complete pair before the terminal usage event.
func TestKoraiCompleteToolCall(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"x","object":"chat.completion","model":"auto",
			"choices":[{"index":0,"message":{"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"ReadFile","arguments":"{\"path\":\"main.go\"}"}}]},
				"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}
		}`)
	}))
	defer srv.Close()

	c := NewKoraiClient("sk-test", srv.URL, "auto")
	ch, err := c.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "read main.go"}}}},
		Tools:    []ToolDef{{Name: "ReadFile", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := drainEvents(t, ch)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}
	start, ok := got[0].(ToolCallStartEvent)
	if !ok || start.ID != "call_1" || start.Name != "ReadFile" {
		t.Errorf("event[0] = %+v, want ToolCallStart call_1/ReadFile", got[0])
	}
	complete, ok := got[1].(ToolCallCompleteEvent)
	if !ok || complete.ID != "call_1" {
		t.Fatalf("event[1] = %+v, want ToolCallComplete call_1", got[1])
	}
	var input map[string]any
	if err := json.Unmarshal(complete.Input, &input); err != nil {
		t.Fatalf("tool input not valid json: %v", err)
	}
	if input["path"] != "main.go" {
		t.Errorf("tool input = %+v, want path=main.go", input)
	}
	if done, ok := got[2].(MessageCompleteEvent); !ok || done.Usage.InputTokens != 20 {
		t.Errorf("event[2] = %+v, want MessageComplete with usage", got[2])
	}
}

// TestKoraiCompleteAPIError verifies a non-2xx response surfaces as an
// ErrorEvent on the channel rather than a panic or silent close.
func TestKoraiCompleteAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key","type":"auth_error"}}`)
	}))
	defer srv.Close()

	c := NewKoraiClient("sk-bad", srv.URL, "auto")
	ch, err := c.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Complete returned error before draining: %v", err)
	}

	got := drainEvents(t, ch)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 error event: %+v", len(got), got)
	}
	if _, ok := got[0].(ErrorEvent); !ok {
		t.Errorf("event[0] = %+v, want ErrorEvent", got[0])
	}
}
