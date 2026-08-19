package apiclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// resolve() consults the opt-in before anything else, so every capability test
// must set it. Uses t.Setenv, which restores the previous value on cleanup.
func enableNativeTools(t *testing.T) {
	t.Helper()
	t.Setenv(nativeToolsEnvVar, "1")
}

func schema(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("test schema is not valid JSON: %s", s)
	}
	return json.RawMessage(s)
}

// The native path must render an OpenAI function tool and carry the caller's
// JSON Schema through verbatim — a rewritten schema silently drops whatever
// schema features we forgot to model.
func TestConvertToNativeTools_ShapeAndVerbatimSchema(t *testing.T) {
	in := schema(t, `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	got, err := convertToNativeTools([]ToolDef{{Name: "read", Description: "Read a file", InputSchema: in}})
	if err != nil {
		t.Fatalf("convertToNativeTools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}

	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "function" {
		t.Errorf("type = %q, want %q", decoded.Type, "function")
	}
	if decoded.Function.Name != "read" {
		t.Errorf("name = %q, want %q", decoded.Function.Name, "read")
	}
	// additionalProperties is the canary: it survives only if we passed the
	// schema through instead of reconstructing it.
	if !strings.Contains(string(decoded.Function.Parameters), `"additionalProperties":false`) {
		t.Errorf("schema was not carried verbatim: %s", decoded.Function.Parameters)
	}
}

// A tool with no inputs still needs a parameters object; some engines reject a
// tool whose parameters are absent.
func TestConvertToNativeTools_EmptySchemaGetsAnObject(t *testing.T) {
	got, err := convertToNativeTools([]ToolDef{{Name: "now"}})
	if err != nil {
		t.Fatalf("convertToNativeTools: %v", err)
	}
	raw, _ := json.Marshal(got[0])
	if !strings.Contains(string(raw), `"type":"object"`) {
		t.Errorf("empty schema did not become an object: %s", raw)
	}
}

func TestConvertToNativeTools_RejectsInvalidSchema(t *testing.T) {
	_, err := convertToNativeTools([]ToolDef{{Name: "bad", InputSchema: json.RawMessage(`{nope`)}})
	if err == nil {
		t.Fatal("want an error for a malformed schema, got nil")
	}
}

// Tool results must become role="tool" messages keyed by tool_call_id, and
// they must precede the user's own text for that turn — OpenAI requires every
// tool result to follow the assistant turn that asked for it.
func TestConvertToNativeMessages_ToolResultsPrecedeUserText(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			TextBlock{Text: "Looking."},
			ToolCallBlock{ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "call_1", Content: "package main"},
			TextBlock{Text: "now explain it"},
		}},
	}

	got, err := convertToNativeMessages(msgs)
	if err != nil {
		t.Fatalf("convertToNativeMessages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(got), got)
	}

	if got[0].Role != "assistant" || len(got[0].ToolCalls) != 1 {
		t.Fatalf("first message should be the assistant turn with 1 tool call: %+v", got[0])
	}
	if got[0].ToolCalls[0].ID != "call_1" || got[0].ToolCalls[0].Name != "read" {
		t.Errorf("tool call = %+v, want id=call_1 name=read", got[0].ToolCalls[0])
	}
	if got[0].ToolCalls[0].Input["path"] != "a.go" {
		t.Errorf("tool call input = %+v, want path=a.go", got[0].ToolCalls[0].Input)
	}

	if got[1].Role != "tool" {
		t.Errorf("tool result must come before the user text; got role %q", got[1].Role)
	}
	if got[1].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1", got[1].ToolCallID)
	}
	if got[2].Role != "user" || got[2].Content != "now explain it" {
		t.Errorf("third message = %+v, want the user text", got[2])
	}
}

// A turn carrying only tool results must NOT emit a trailing empty user
// message. The fence path does that to preserve turn count; here it would be
// a protocol error.
func TestConvertToNativeMessages_ToolOnlyTurnEmitsNoEmptyUser(t *testing.T) {
	got, err := convertToNativeMessages([]Message{
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "call_1", Content: "ok"},
		}},
	})
	if err != nil {
		t.Fatalf("convertToNativeMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want exactly the tool result: %+v", len(got), got)
	}
	if got[0].Role != "tool" {
		t.Errorf("role = %q, want tool", got[0].Role)
	}
}

func TestConvertToNativeMessages_ErrorResultIsMarked(t *testing.T) {
	got, err := convertToNativeMessages([]Message{
		{Role: RoleUser, Content: []ContentBlock{
			ToolResultBlock{ToolCallID: "c1", Content: "no such file", IsError: true},
		}},
	})
	if err != nil {
		t.Fatalf("convertToNativeMessages: %v", err)
	}
	if !strings.HasPrefix(got[0].Content, "ERROR: ") {
		t.Errorf("error result not marked: %q", got[0].Content)
	}
}

// A no-argument tool call records nothing; that must be an empty object, not
// a decode failure that kills the whole turn.
func TestToolInputMap_EmptyIsAnObjectNotAnError(t *testing.T) {
	for _, in := range []string{"", "null", "  "} {
		got, err := toolInputMap(json.RawMessage(in))
		if err != nil {
			t.Fatalf("toolInputMap(%q): %v", in, err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("toolInputMap(%q) = %+v, want empty map", in, got)
		}
	}
	if _, err := toolInputMap(json.RawMessage(`"a string"`)); err == nil {
		t.Error("want an error for non-object input, got nil")
	}
}

// THE invariant. The two dialects must never both appear in one request: a
// model told about its tools in the system prompt AND in the tools array
// answers in both, and runBuffered honours structured calls over fences,
// silently discarding the fence ones. Native requests must therefore carry a
// tools array and NO fence instructions; fence requests the reverse.
func TestDialectsAreMutuallyExclusive(t *testing.T) {
	c := &KoraiClient{model: "m"}
	req := Request{
		System: "You are a helpful assistant.",
		Tools: []ToolDef{{
			Name:        "read",
			Description: "Read a file",
			InputSchema: schema(t, `{"type":"object","properties":{}}`),
		}},
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: "hi"}}}},
	}

	native, err := c.buildNativeChatRequest(req, "m", 100)
	if err != nil {
		t.Fatalf("buildNativeChatRequest: %v", err)
	}
	if len(native.Tools) == 0 {
		t.Error("native request carries no tools array")
	}
	for _, m := range native.Messages {
		if strings.Contains(m.Content, fenceOpenPrefix) || strings.Contains(m.Content, "<tool:") {
			t.Errorf("native request leaked fence instructions into a message: %q", m.Content)
		}
	}

	fence, err := c.buildChatRequest(req, "m", 100)
	if err != nil {
		t.Fatalf("buildChatRequest: %v", err)
	}
	if len(fence.Tools) != 0 {
		t.Error("fence request must not carry a tools array")
	}
	var sawInstructions bool
	for _, m := range fence.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "read") {
			sawInstructions = true
		}
	}
	if !sawInstructions {
		t.Error("fence request lost its tool instructions")
	}
}

// An unprobed client (no orchestrator reachable, old server, model not listed)
// must fall back to the fence. Guessing native against a stack that ignores
// the tools array yields prose where a tool call was needed.
func TestToolCapabilities_UnknownFallsBackToFence(t *testing.T) {
	enableNativeTools(t)

	// The Once must be spent as well as the fields set — otherwise resolve
	// still runs the real probe. (It did, on a nil client, when this test was
	// first written.)
	newProbed := func(m map[string]bool, probed bool) *toolCapabilities {
		tc := &toolCapabilities{byID: m, probed: probed}
		tc.once.Do(func() {})
		return tc
	}

	tc := newProbed(map[string]bool{"has-tools": true, "no-tools": false}, true)

	if got := tc.resolve(context.TODO(), nil, "has-tools"); got != toolModeNative {
		t.Errorf("has-tools = %v, want native", got)
	}
	if got := tc.resolve(context.TODO(), nil, "no-tools"); got != toolModeFence {
		t.Errorf("no-tools = %v, want fence", got)
	}
	if got := tc.resolve(context.TODO(), nil, "never-heard-of-it"); got != toolModeFence {
		t.Errorf("unlisted model = %v, want fence", got)
	}

	unprobed := newProbed(nil, false) // probe failed: once spent, probed false
	if got := unprobed.resolve(context.TODO(), nil, "anything"); got != toolModeFence {
		t.Errorf("failed probe = %v, want fence", got)
	}
}

// The server-side gate. supports_tools describes the MODEL; it is already true
// for the current ladder while the deployed orchestrator still drops `tools`.
// Without this gate the CLI would go native against production, sending tools
// nothing reads and omitting the fence instructions the model needs — every
// tool call would silently vanish.
func TestNativeToolsRequireTheOptIn(t *testing.T) {
	tc := &toolCapabilities{byID: map[string]bool{"m": true}, probed: true}
	tc.once.Do(func() {})

	t.Setenv(nativeToolsEnvVar, "")
	if got := tc.resolve(context.TODO(), nil, "m"); got != toolModeFence {
		t.Errorf("opt-in unset: got %v, want fence even though the model supports tools", got)
	}
	t.Setenv(nativeToolsEnvVar, "0")
	if got := tc.resolve(context.TODO(), nil, "m"); got != toolModeFence {
		t.Errorf("opt-in=0: got %v, want fence", got)
	}
	t.Setenv(nativeToolsEnvVar, "1")
	if got := tc.resolve(context.TODO(), nil, "m"); got != toolModeNative {
		t.Errorf("opt-in=1 with a tool-capable model: got %v, want native", got)
	}
}
