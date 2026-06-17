package bash_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/bash"
)

// mustRaw marshals v into json.RawMessage, failing the test on error.
func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return b
}

func TestDeclarations(t *testing.T) {
	tl := bash.New()
	if got := tl.Name(); got != "Bash" {
		t.Errorf("Name() = %q, want %q", got, "Bash")
	}
	if tl.ReadOnly() {
		t.Error("ReadOnly() = true, want false")
	}
	if tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = true, want false")
	}
	if tl.InputSchema() == nil {
		t.Error("InputSchema() = nil, want non-nil")
	}
}

func TestCheckPermission(t *testing.T) {
	tl := bash.New()
	tests := []struct {
		name string
		mode perm.Mode
		want perm.Decision
	}{
		{"bypass", perm.ModeBypassPermissions, perm.DecisionAllow},
		{"plan", perm.ModePlan, perm.DecisionDeny},
		{"default", perm.ModeDefault, perm.DecisionAsk},
		{"acceptEdits", perm.ModeAcceptEdits, perm.DecisionAsk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tl.CheckPermission(context.Background(), nil, tc.mode)
			if got != tc.want {
				t.Errorf("CheckPermission(%v) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

func TestExecuteSuccess(t *testing.T) {
	tl := bash.New()
	raw := mustRaw(t, bash.Input{Command: "echo hello"})
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false; content=%q", res.Content)
	}
	if got := strings.TrimSpace(res.Content); got != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

func TestExecuteCombinesStderr(t *testing.T) {
	tl := bash.New()
	raw := mustRaw(t, bash.Input{Command: "echo out; echo err 1>&2"})
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false; content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "out") || !strings.Contains(res.Content, "err") {
		t.Errorf("content = %q, want both stdout and stderr", res.Content)
	}
}

func TestExecuteWorkingDirHonored(t *testing.T) {
	tl := bash.New()
	dir := t.TempDir()
	raw := mustRaw(t, bash.Input{Command: "pwd > marker.txt"})
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content=%q", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(dir, "marker.txt"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	// On macOS TempDir may be a symlink (/var -> /private/var); resolve both
	// before comparing so the assertion is portable.
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if diff := cmp.Diff(wantDir, gotDir); diff != "" {
		t.Errorf("pwd mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	tl := bash.New()
	raw := mustRaw(t, bash.Input{Command: "false"})
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (soft error)", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for non-zero exit")
	}
	if !strings.Contains(res.Content, "exited with code") {
		t.Errorf("content = %q, want exit-code note", res.Content)
	}
}

func TestExecuteTimeout(t *testing.T) {
	tl := bash.New()
	raw := mustRaw(t, bash.Input{Command: "sleep 5", TimeoutMS: 50})
	start := time.Now()
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (soft error)", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true on timeout")
	}
	if !strings.Contains(res.Content, "timed out") {
		t.Errorf("content = %q, want timeout note", res.Content)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v, expected to be cut short by timeout", elapsed)
	}
}

func TestExecuteContextCancellation(t *testing.T) {
	tl := bash.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running
	raw := mustRaw(t, bash.Input{Command: "sleep 5"})
	res, err := tl.Execute(ctx, raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (soft error)", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true on cancellation")
	}
}

func TestExecuteEmptyCommand(t *testing.T) {
	tl := bash.New()
	raw := mustRaw(t, bash.Input{Command: ""})
	res, err := tl.Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (soft error)", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true for empty command")
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	tl := bash.New()
	res, err := tl.Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatalf("Execute() error = nil, want hard error for invalid JSON")
	}
	if res.IsError {
		t.Errorf("IsError = true, want false (hard error returns zero Result)")
	}
}
