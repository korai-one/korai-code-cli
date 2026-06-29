package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// fakeDiagnoser is a stub Diagnoser for testing the tool without a real server.
type fakeDiagnoser struct {
	enabled bool
	report  string
}

func (f fakeDiagnoser) Enabled() bool { return f.enabled }
func (f fakeDiagnoser) DiagnoseFile(_ context.Context, _, _ string, _ time.Duration) string {
	return f.report
}

func run(t *testing.T, d Diagnoser, dir string, in Input) tool.Result {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := New(d).Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestExecute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("path required", func(t *testing.T) {
		res := run(t, fakeDiagnoser{enabled: true}, dir, Input{})
		if !res.IsError {
			t.Error("empty path should be a soft error")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		res := run(t, fakeDiagnoser{enabled: false}, dir, Input{Path: "a.go"})
		if res.IsError || !strings.Contains(res.Content, "unavailable") {
			t.Errorf("disabled LSP should report unavailable, got %+v", res)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		res := run(t, fakeDiagnoser{enabled: true}, dir, Input{Path: "nope.go"})
		if !res.IsError {
			t.Error("missing file should be a soft error")
		}
	})

	t.Run("no diagnostics", func(t *testing.T) {
		res := run(t, fakeDiagnoser{enabled: true, report: ""}, dir, Input{Path: "a.go"})
		if res.IsError || !strings.Contains(res.Content, "no diagnostics") {
			t.Errorf("empty report should say no diagnostics, got %+v", res)
		}
	})

	t.Run("with diagnostics", func(t *testing.T) {
		res := run(t, fakeDiagnoser{enabled: true, report: "<file_diagnostics>boom</file_diagnostics>"}, dir, Input{Path: "a.go"})
		if res.IsError || !strings.Contains(res.Content, "boom") {
			t.Errorf("expected the report passed through, got %+v", res)
		}
	})
}

func TestReadOnlyAllowed(t *testing.T) {
	tl := New(fakeDiagnoser{})
	if !tl.ReadOnly() || !tl.ConcurrencySafe() {
		t.Error("lsp_diagnostics must be read-only and concurrency-safe")
	}
}
