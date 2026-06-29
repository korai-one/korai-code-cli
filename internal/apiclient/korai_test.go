package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestBuildChatRequestSystemAsMessage verifies the system prompt (including the
// fence tool instructions) is sent as a leading role="system" message, not the
// top-level System field — Korai's endpoints have no such field and would drop
// it, taking the tool instructions with it.
func TestBuildChatRequestSystemAsMessage(t *testing.T) {
	t.Parallel()

	c := &KoraiClient{model: "auto"}
	cr, err := c.buildChatRequest(Request{
		System:   "BASE PROMPT",
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}}},
		Tools:    []ToolDef{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	if cr.System != "" {
		t.Errorf("top-level System must be empty (endpoints ignore it), got %q", cr.System)
	}
	if len(cr.Messages) != 2 {
		t.Fatalf("got %d messages, want system + user", len(cr.Messages))
	}
	if cr.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", cr.Messages[0].Role)
	}
	if !strings.Contains(cr.Messages[0].Content, "BASE PROMPT") {
		t.Error("system message missing the base prompt")
	}
	if !strings.Contains(cr.Messages[0].Content, "read_file") {
		t.Error("system message missing the fence tool instructions")
	}
	if cr.Messages[1].Role != "user" {
		t.Errorf("second message role = %q, want the user turn", cr.Messages[1].Role)
	}
}

// TestConvertToKoraiMessages verifies that block-structured messages flatten
// into Korai's fence dialect: user text, an assistant turn whose tool call is
// rendered as a <tool:…> fence appended to its content, and a tool result
// rendered as [TOOL RESULT: name] text in a user message (no role="tool", no
// structured ToolCalls).
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
	if got[1].Role != "assistant" || got[1].Content != "ok\n<tool:ReadFile>{\"path\":\"x\"}</tool>" {
		t.Errorf("msg[1] content = %q, want text + fence", got[1].Content)
	}
	if len(got[1].ToolCalls) != 0 {
		t.Errorf("msg[1] must carry no structured ToolCalls, got %+v", got[1].ToolCalls)
	}
	if got[2].Role != "user" || got[2].Content != "[TOOL RESULT: ReadFile]\ndata" {
		t.Errorf("msg[2] = %+v, want user tool-result text for ReadFile", got[2])
	}
}

// TestConvertToKoraiMessagesErrorResult verifies an error tool result renders
// with the [TOOL ERROR: name] label the model sees.
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
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(got), got)
	}
	if got[0].Content != "<tool:Bash>{}</tool>" {
		t.Errorf("assistant content = %q, want the Bash fence", got[0].Content)
	}
	if got[1].Content != "[TOOL ERROR: Bash]\nboom" {
		t.Errorf("error result content = %q, want \"[TOOL ERROR: Bash]\\nboom\"", got[1].Content)
	}
}

// TestConvertToKoraiMessagesImage checks a user message carrying an ImageBlock
// becomes a multimodal content-parts message (text part + image_url part).
func TestConvertToKoraiMessagesImage(t *testing.T) {
	t.Parallel()

	got, err := convertToKoraiMessages([]Message{
		{Role: RoleUser, Content: []ContentBlock{
			TextBlock{Text: "what is this?"},
			ImageBlock{Source: "data:image/png;base64,AAAA"},
		}},
	})
	if err != nil {
		t.Fatalf("convertToKoraiMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1: %+v", len(got), got)
	}
	if got[0].Content != "" {
		t.Errorf("multimodal message should leave the flat Content empty, got %q", got[0].Content)
	}
	if len(got[0].Parts) != 2 {
		t.Fatalf("want 2 content parts, got %d: %+v", len(got[0].Parts), got[0].Parts)
	}
	if got[0].Parts[0].Type != "text" || got[0].Parts[0].Text != "what is this?" {
		t.Errorf("text part = %+v", got[0].Parts[0])
	}
	img := got[0].Parts[1]
	if img.Type != "image_url" || img.ImageURL == nil || img.ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Errorf("image part = %+v", img)
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

// TestKoraiCompleteFenceToolCall verifies the primary Korai path: a reply whose
// text carries a <tool:…> fence (and no structured tool_calls) is parsed into a
// synthesized start+complete pair, with the surrounding prose emitted as text.
func TestKoraiCompleteFenceToolCall(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"x","object":"chat.completion","model":"auto",
			"choices":[{"index":0,"message":{"role":"assistant",
				"content":"Je lis le fichier.\n<tool:ReadFile>{\"path\":\"main.go\"}</tool>"},
				"finish_reason":"stop"}],
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
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4 (text, start, complete, done): %+v", len(got), got)
	}
	if txt, ok := got[0].(TextDeltaEvent); !ok || txt.Text != "Je lis le fichier." {
		t.Errorf("event[0] = %+v, want the prose with the fence stripped", got[0])
	}
	start, ok := got[1].(ToolCallStartEvent)
	if !ok || start.Name != "ReadFile" || start.ID == "" {
		t.Fatalf("event[1] = %+v, want ToolCallStart ReadFile with a synthesized id", got[1])
	}
	complete, ok := got[2].(ToolCallCompleteEvent)
	if !ok || complete.ID != start.ID || complete.Name != "ReadFile" {
		t.Fatalf("event[2] = %+v, want ToolCallComplete sharing the start id", got[2])
	}
	var input map[string]any
	if err := json.Unmarshal(complete.Input, &input); err != nil {
		t.Fatalf("tool input not valid json: %v", err)
	}
	if input["path"] != "main.go" {
		t.Errorf("tool input = %+v, want path=main.go", input)
	}
	if _, ok := got[3].(MessageCompleteEvent); !ok {
		t.Errorf("event[3] = %+v, want MessageComplete", got[3])
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
