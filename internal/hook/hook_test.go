package hook_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/hook"
)

func TestFireBlocking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hooks     map[string][]hook.Spec
		event     string
		toolName  string
		input     json.RawMessage
		wantBlock bool
	}{
		{
			name:      "PreToolUse non-zero exit blocks",
			hooks:     map[string][]hook.Spec{hook.EventPreToolUse: {{Command: "exit 1"}}},
			event:     hook.EventPreToolUse,
			wantBlock: true,
		},
		{
			name:      "PreToolUse zero exit does not block",
			hooks:     map[string][]hook.Spec{hook.EventPreToolUse: {{Command: "exit 0"}}},
			event:     hook.EventPreToolUse,
			wantBlock: false,
		},
		{
			name:      "PreToolUse failed launch blocks",
			hooks:     map[string][]hook.Spec{hook.EventPreToolUse: {{Command: "false"}}},
			event:     hook.EventPreToolUse,
			wantBlock: true,
		},
		{
			name:      "PostToolUse non-zero exit is observe-only",
			hooks:     map[string][]hook.Spec{hook.EventPostToolUse: {{Command: "exit 1"}}},
			event:     hook.EventPostToolUse,
			wantBlock: false,
		},
		{
			name:      "SessionStart non-zero exit is observe-only",
			hooks:     map[string][]hook.Spec{hook.EventSessionStart: {{Command: "exit 1"}}},
			event:     hook.EventSessionStart,
			wantBlock: false,
		},
		{
			name:      "no hooks for event is a no-op",
			hooks:     map[string][]hook.Spec{hook.EventPostToolUse: {{Command: "exit 1"}}},
			event:     hook.EventPreToolUse,
			wantBlock: false,
		},
		{
			name:      "empty command is skipped",
			hooks:     map[string][]hook.Spec{hook.EventPreToolUse: {{Command: ""}}},
			event:     hook.EventPreToolUse,
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := hook.New(tt.hooks)
			block, _ := r.Fire(context.Background(), tt.event, tt.toolName, tt.input)
			if diff := cmp.Diff(tt.wantBlock, block); diff != "" {
				t.Errorf("block mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFireBlockingReason(t *testing.T) {
	t.Parallel()

	r := hook.New(map[string][]hook.Spec{
		hook.EventPreToolUse: {{Command: "echo nope; exit 1"}},
	})
	block, reason := r.Fire(context.Background(), hook.EventPreToolUse, "Bash", nil)
	if !block {
		t.Fatalf("expected block=true, got false")
	}
	if !strings.Contains(reason, "nope") {
		t.Errorf("reason = %q, want it to contain %q", reason, "nope")
	}
}

func TestFireDefaultReason(t *testing.T) {
	t.Parallel()

	r := hook.New(map[string][]hook.Spec{
		hook.EventPreToolUse: {{Command: "exit 1"}},
	})
	block, reason := r.Fire(context.Background(), hook.EventPreToolUse, "Bash", nil)
	if !block {
		t.Fatalf("expected block=true, got false")
	}
	if reason == "" {
		t.Errorf("reason = %q, want a non-empty default message", reason)
	}
}

func TestFireStopsAtFirstBlock(t *testing.T) {
	t.Parallel()

	// The first hook blocks with "first"; the second would block with "second".
	// Fire must stop at the first and report its reason.
	r := hook.New(map[string][]hook.Spec{
		hook.EventPreToolUse: {
			{Command: "echo first; exit 1"},
			{Command: "echo second; exit 1"},
		},
	})
	block, reason := r.Fire(context.Background(), hook.EventPreToolUse, "Bash", nil)
	if !block {
		t.Fatalf("expected block=true, got false")
	}
	if !strings.Contains(reason, "first") || strings.Contains(reason, "second") {
		t.Errorf("reason = %q, want only the first hook's output", reason)
	}
}

func TestFireEnvPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		command   string
		toolName  string
		input     json.RawMessage
		wantBlock bool
	}{
		{
			name:      "matching tool name triggers block",
			command:   `[ "$KORAI_TOOL_NAME" = "Bash" ] && exit 1; exit 0`,
			toolName:  "Bash",
			wantBlock: true,
		},
		{
			name:      "non-matching tool name does not block",
			command:   `[ "$KORAI_TOOL_NAME" = "Bash" ] && exit 1; exit 0`,
			toolName:  "Edit",
			wantBlock: false,
		},
		{
			name:      "event var propagates",
			command:   `[ "$KORAI_EVENT" = "PreToolUse" ] && exit 1; exit 0`,
			toolName:  "Bash",
			wantBlock: true,
		},
		{
			name:      "input var propagates",
			command:   `[ "$KORAI_TOOL_INPUT" = "{\"x\":1}" ] && exit 1; exit 0`,
			toolName:  "Bash",
			input:     json.RawMessage(`{"x":1}`),
			wantBlock: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := hook.New(map[string][]hook.Spec{
				hook.EventPreToolUse: {{Command: tt.command}},
			})
			block, _ := r.Fire(context.Background(), hook.EventPreToolUse, tt.toolName, tt.input)
			if diff := cmp.Diff(tt.wantBlock, block); diff != "" {
				t.Errorf("block mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFireNilRunner(t *testing.T) {
	t.Parallel()

	var r *hook.Runner
	block, reason := r.Fire(context.Background(), hook.EventPreToolUse, "Bash", nil)
	if block || reason != "" {
		t.Errorf("nil runner: got (%v, %q), want (false, \"\")", block, reason)
	}
}

func TestFireNilMap(t *testing.T) {
	t.Parallel()

	r := hook.New(nil)
	block, reason := r.Fire(context.Background(), hook.EventPreToolUse, "Bash", nil)
	if block || reason != "" {
		t.Errorf("nil map: got (%v, %q), want (false, \"\")", block, reason)
	}
}
