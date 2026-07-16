package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// decode marshals evt via encodeEvent and unmarshals the single line back into a
// generic map so tests can assert individual fields without depending on Go's
// struct-field ordering.
func decode(t *testing.T, evt engine.Event) map[string]any {
	t.Helper()
	line, err := encodeEvent(evt)
	if err != nil {
		t.Fatalf("encodeEvent(%T): %v", evt, err)
	}
	if bytes.ContainsRune(line, '\n') {
		t.Fatalf("encodeEvent(%T): line contains an embedded newline: %q", evt, line)
	}
	var got map[string]any
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	return got
}

func TestEncodeEventText(t *testing.T) {
	got := decode(t, engine.TextEvent{Text: "hello\nworld"})
	want := map[string]any{"type": "text", "text": "hello\nworld"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("text event mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventToolStart(t *testing.T) {
	got := decode(t, engine.ToolStartEvent{
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"ls"}`),
	})
	want := map[string]any{
		"type":  "tool_start",
		"name":  "Bash",
		"input": map[string]any{"command": "ls"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tool_start event mismatch (-want +got):\n%s", diff)
	}
}

// TestEncodeEventToolStartNilInput checks that a missing raw input encodes as a
// JSON null rather than invalid JSON or an omitted field.
func TestEncodeEventToolStartNilInput(t *testing.T) {
	got := decode(t, engine.ToolStartEvent{Name: "Glob"})
	want := map[string]any{"type": "tool_start", "name": "Glob", "input": nil}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tool_start nil-input mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventToolResultError(t *testing.T) {
	got := decode(t, engine.ToolResultEvent{
		Name:   "ReadFile",
		Result: tool.Result{Content: "boom", IsError: true},
	})
	want := map[string]any{
		"type":     "tool_result",
		"name":     "ReadFile",
		"content":  "boom",
		"is_error": true,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tool_result (error) mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventToolResultOK(t *testing.T) {
	got := decode(t, engine.ToolResultEvent{
		Name:   "ReadFile",
		Result: tool.Result{Content: "ok", IsError: false},
	})
	want := map[string]any{
		"type":     "tool_result",
		"name":     "ReadFile",
		"content":  "ok",
		"is_error": false,
	}
	// is_error must be present (not omitted) even when false, so consumers can
	// rely on a stable schema.
	if _, ok := got["is_error"]; !ok {
		t.Errorf("tool_result (ok) missing is_error field: %v", got)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tool_result (ok) mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventCompacted(t *testing.T) {
	got := decode(t, engine.CompactedEvent{Before: 40, After: 12})
	want := map[string]any{"type": "compacted", "before": 40.0, "after": 12.0}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("compacted event mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventDone(t *testing.T) {
	got := decode(t, engine.DoneEvent{Messages: []apiclient.Message{
		{Role: apiclient.RoleUser},
		{Role: apiclient.RoleAssistant},
	}})
	want := map[string]any{"type": "done", "messages": 2.0}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("done event mismatch (-want +got):\n%s", diff)
	}
}

func TestEncodeEventError(t *testing.T) {
	got := decode(t, engine.ErrorEvent{Err: errors.New("stream failed")})
	want := map[string]any{"type": "error", "error": "stream failed"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("error event mismatch (-want +got):\n%s", diff)
	}
}

// TestEncodeEventExactBytes pins the exact JSON for a couple of events so the
// wire schema stays stable across refactors.
func TestEncodeEventExactBytes(t *testing.T) {
	cases := []struct {
		name string
		evt  engine.Event
		want string
	}{
		{
			name: "text",
			evt:  engine.TextEvent{Text: "hi"},
			want: `{"type":"text","text":"hi"}`,
		},
		{
			name: "tool_result_ok",
			evt:  engine.ToolResultEvent{Name: "Glob", Result: tool.Result{Content: "a.go"}},
			want: `{"type":"tool_result","name":"Glob","content":"a.go","is_error":false}`,
		},
		{
			name: "done",
			evt:  engine.DoneEvent{},
			want: `{"type":"done","messages":0}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeEvent(tc.evt)
			if err != nil {
				t.Fatalf("encodeEvent: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestValidateOutputFormat(t *testing.T) {
	for _, ok := range []string{"text", "json"} {
		if err := validateOutputFormat(ok); err != nil {
			t.Errorf("validateOutputFormat(%q) = %v, want nil", ok, err)
		}
	}
	if err := validateOutputFormat("yaml"); err == nil {
		t.Errorf("validateOutputFormat(%q) = nil, want error", "yaml")
	}
}
