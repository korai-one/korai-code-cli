package check_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/check"
)

// passCmd returns a shell command that exits 0 on the test's platform.
func passCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 0"
	}
	return "exit 0"
}

// failCmd returns a shell command that exits 1 on the test's platform.
func failCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "exit 1"
}

func TestDeclarations(t *testing.T) {
	tl := check.New(nil)
	if got := tl.Name(); got != "RunChecks" {
		t.Errorf("Name() = %q, want %q", got, "RunChecks")
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
	tl := check.New(nil)
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

func TestExecuteAllPass(t *testing.T) {
	tl := check.New([]string{passCmd(), passCmd()})
	res, err := tl.Execute(context.Background(), nil, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false; content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "PASS") {
		t.Errorf("content = %q, want a PASS line", res.Content)
	}
	if strings.Contains(res.Content, "FAIL") {
		t.Errorf("content = %q, want no FAIL line", res.Content)
	}
}

func TestExecuteFailure(t *testing.T) {
	tl := check.New([]string{passCmd(), failCmd()})
	res, err := tl.Execute(context.Background(), nil, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (soft error)", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true when a check fails")
	}
	if !strings.Contains(res.Content, "FAIL") {
		t.Errorf("content = %q, want a FAIL line", res.Content)
	}
	// The full report still includes the earlier passing check.
	if !strings.Contains(res.Content, "PASS") {
		t.Errorf("content = %q, want the passing check reported too", res.Content)
	}
}

func TestExecuteNoChecks(t *testing.T) {
	tl := check.New(nil)
	res, err := tl.Execute(context.Background(), nil, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false when no checks configured")
	}
	if !strings.Contains(res.Content, "no checks are configured") {
		t.Errorf("content = %q, want a friendly no-checks message", res.Content)
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	tl := check.New([]string{passCmd()})
	res, err := tl.Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatalf("Execute() error = nil, want hard error for invalid JSON")
	}
	if res.IsError {
		t.Errorf("IsError = true, want false (hard error returns zero Result)")
	}
}
