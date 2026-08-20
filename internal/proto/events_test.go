package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/proto"
)

// TestServerEventJSON checks each server event marshals to the agreed wire
// shape: the "type" discriminator plus its payload fields. The client routes on
// "type", so these tags are the contract.
func TestServerEventJSON(t *testing.T) {
	tests := []struct {
		name  string
		event proto.ServerEvent
		want  map[string]any
	}{
		{
			name:  "text",
			event: proto.Text("hello"),
			want:  map[string]any{"type": "text", "delta": "hello"},
		},
		{
			name:  "tool_start",
			event: proto.ToolStart("id-1", "bash", json.RawMessage(`{"cmd":"ls"}`)),
			want:  map[string]any{"type": "tool_start", "id": "id-1", "name": "bash", "input": map[string]any{"cmd": "ls"}},
		},
		{
			name:  "tool_result",
			event: proto.ToolResult("id-1", "bash", "file.txt", false),
			want:  map[string]any{"type": "tool_result", "id": "id-1", "name": "bash", "content": "file.txt", "is_error": false},
		},
		{
			name:  "perm_req",
			event: proto.PermReq("p-1", "write", json.RawMessage(`{"path":"a"}`)),
			want:  map[string]any{"type": "perm_req", "id": "p-1", "tool": "write", "input": map[string]any{"path": "a"}},
		},
		{
			name:  "compact",
			event: proto.Compact(42, 12),
			want:  map[string]any{"type": "compact", "before": float64(42), "after": float64(12)},
		},
		{
			name:  "error",
			event: proto.Error("boom"),
			want:  map[string]any{"type": "error", "message": "boom"},
		},
		{
			name:  "done",
			event: proto.Done(),
			want:  map[string]any{"type": "done"},
		},
		{
			name: "sessions",
			event: proto.Sessions([]proto.SessionSummary{
				{ID: "s-1", Created: "2026-01-01T00:00:00Z", Updated: "2026-01-02T00:00:00Z",
					CWD: "/work", Model: "auto", Tool: "korai-code-cli", Title: "fix the bug", MessageCount: 4},
			}),
			want: map[string]any{"type": "sessions", "sessions": []any{
				map[string]any{
					"id": "s-1", "created": "2026-01-01T00:00:00Z", "updated": "2026-01-02T00:00:00Z",
					"cwd": "/work", "model": "auto", "tool": "korai-code-cli", "title": "fix the bug",
					"messageCount": float64(4),
				},
			}},
		},
		{
			name:  "resumed",
			event: proto.Resumed("s-1", []json.RawMessage{[]byte(`{"role":"user","blocks":[{"kind":"text","text":"hi"}]}`)}),
			want: map[string]any{"type": "resumed", "id": "s-1", "messages": []any{
				map[string]any{"role": "user", "blocks": []any{map[string]any{"kind": "text", "text": "hi"}}},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("wire shape mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClientMsgUnmarshal checks each client message decodes into the single
// ClientMsg struct with the right fields populated.
func TestClientMsgUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want proto.ClientMsg
	}{
		{
			name: "message",
			raw:  `{"type":"message","text":"liste les fichiers"}`,
			want: proto.ClientMsg{Type: "message", Text: "liste les fichiers"},
		},
		{
			name: "perm_res",
			raw:  `{"type":"perm_res","id":"p-1","approved":true}`,
			want: proto.ClientMsg{Type: "perm_res", ID: "p-1", Approved: true},
		},
		{
			name: "slash",
			raw:  `{"type":"slash","cmd":"compact","text":""}`,
			want: proto.ClientMsg{Type: "slash", Cmd: "compact"},
		},
		{
			name: "abort",
			raw:  `{"type":"abort"}`,
			want: proto.ClientMsg{Type: "abort"},
		},
		{
			name: "sessions_list",
			raw:  `{"type":"sessions_list","cwd":"/work","limit":10}`,
			want: proto.ClientMsg{Type: "sessions_list", Cwd: "/work", Limit: 10},
		},
		{
			name: "sessions_list bare",
			raw:  `{"type":"sessions_list"}`,
			want: proto.ClientMsg{Type: "sessions_list"},
		},
		{
			name: "sessions_resume",
			raw:  `{"type":"sessions_resume","id":"s-1"}`,
			want: proto.ClientMsg{Type: "sessions_resume", ID: "s-1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got proto.ClientMsg
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ClientMsg mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
