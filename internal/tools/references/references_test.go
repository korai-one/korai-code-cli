package references

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

// fakeReferencer is a stub Referencer for testing the tool without a real server.
type fakeReferencer struct {
	enabled bool
	out     string
	err     error
}

func (f fakeReferencer) Enabled() bool { return f.enabled }
func (f fakeReferencer) ReferencesText(_ context.Context, _, _ string, _, _ int, _ bool, _ time.Duration) (string, error) {
	return f.out, f.err
}

func run(t *testing.T, r Referencer, dir string, in Input) tool.Result {
	t.Helper()
	raw, _ := json.Marshal(in)
	res, err := New(r).Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestExecute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("path required", func(t *testing.T) {
		res := run(t, fakeReferencer{enabled: true}, dir, Input{Line: 3})
		if !res.IsError {
			t.Error("empty path should be a soft error")
		}
	})

	t.Run("line required", func(t *testing.T) {
		res := run(t, fakeReferencer{enabled: true}, dir, Input{Path: "a.go"})
		if !res.IsError {
			t.Error("missing line should be a soft error")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		res := run(t, fakeReferencer{enabled: false}, dir, Input{Path: "a.go", Line: 3})
		if res.IsError || !strings.Contains(res.Content, "unavailable") {
			t.Errorf("disabled LSP should report unavailable, got %+v", res)
		}
	})

	t.Run("none found", func(t *testing.T) {
		res := run(t, fakeReferencer{enabled: true, out: ""}, dir, Input{Path: "a.go", Line: 3})
		if res.IsError || !strings.Contains(res.Content, "no references") {
			t.Errorf("empty result should say no references, got %+v", res)
		}
	})

	t.Run("found", func(t *testing.T) {
		res := run(t, fakeReferencer{enabled: true, out: "a.go:3:6\nb.go:9:2"}, dir, Input{Path: "a.go", Line: 3, Column: 6})
		if res.IsError {
			t.Fatalf("unexpected error: %+v", res)
		}
		if !strings.Contains(res.Content, "<references>") || !strings.Contains(res.Content, "b.go:9:2") {
			t.Errorf("expected wrapped reference list, got %s", res.Content)
		}
	})
}

func TestReadOnlyAllowed(t *testing.T) {
	tl := New(fakeReferencer{})
	if !tl.ReadOnly() || !tl.ConcurrencySafe() {
		t.Error("lsp_references must be read-only and concurrency-safe")
	}
}
