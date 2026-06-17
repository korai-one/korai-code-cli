package websearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/websearch"
)

// mockSearcher is a hermetic Searcher used in tests. It records the maxResults
// it received and returns canned results or a canned error.
type mockSearcher struct {
	results []websearch.Result
	err     error

	gotQuery      string
	gotMaxResults int
}

func (m *mockSearcher) Search(_ context.Context, query string, maxResults int) ([]websearch.Result, error) {
	m.gotQuery = query
	m.gotMaxResults = maxResults
	return m.results, m.err
}

func TestDeclarations(t *testing.T) {
	tl := websearch.New()
	if got := tl.Name(); got != "WebSearch" {
		t.Errorf("Name() = %q, want %q", got, "WebSearch")
	}
	if !tl.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	if !tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = false, want true")
	}
	if got := tl.CheckPermission(context.Background(), nil, perm.ModeDefault); got != perm.DecisionAsk {
		t.Errorf("CheckPermission(ModeDefault) = %v, want DecisionAsk", got)
	}
	if got := tl.CheckPermission(context.Background(), nil, perm.ModeBypassPermissions); got != perm.DecisionAllow {
		t.Errorf("CheckPermission(ModeBypassPermissions) = %v, want DecisionAllow", got)
	}
	if tl.InputSchema() == nil {
		t.Error("InputSchema() = nil, want non-nil")
	}
}

func TestExecuteDefaultBackendNotConfigured(t *testing.T) {
	tl := websearch.New()
	res, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"golang"}`), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute() returned hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("Execute() IsError = false, want true; content = %q", res.Content)
	}
	if !containsAll(res.Content, "configured") {
		t.Errorf("Execute() content = %q, want mention of configuration", res.Content)
	}
}

func TestExecuteWithResults(t *testing.T) {
	m := &mockSearcher{
		results: []websearch.Result{
			{Title: "Go", URL: "https://go.dev", Snippet: "The Go language"},
			{Title: "Pkg", URL: "https://pkg.go.dev"},
		},
	}
	tl := websearch.New(websearch.WithSearcher(m))
	res, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"golang","max_results":2}`), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute() returned hard error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute() IsError = true, content = %q", res.Content)
	}
	want := "Go — https://go.dev\n    The Go language\nPkg — https://pkg.go.dev"
	if diff := cmp.Diff(want, res.Content); diff != "" {
		t.Errorf("Execute() content mismatch (-want +got):\n%s", diff)
	}
	if m.gotMaxResults != 2 {
		t.Errorf("searcher got maxResults = %d, want 2", m.gotMaxResults)
	}
	if m.gotQuery != "golang" {
		t.Errorf("searcher got query = %q, want %q", m.gotQuery, "golang")
	}
}

func TestExecuteBackendError(t *testing.T) {
	m := &mockSearcher{err: errors.New("rate limited")}
	tl := websearch.New(websearch.WithSearcher(m))
	res, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"golang"}`), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute() returned hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("Execute() IsError = false, want true; content = %q", res.Content)
	}
	if !containsAll(res.Content, "rate limited") {
		t.Errorf("Execute() content = %q, want mention of backend error", res.Content)
	}
}

func TestExecuteEmptyQuery(t *testing.T) {
	tl := websearch.New(websearch.WithSearcher(&mockSearcher{}))
	res, err := tl.Execute(context.Background(), json.RawMessage(`{"query":""}`), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute() returned hard error: %v", err)
	}
	if !res.IsError {
		t.Errorf("Execute() IsError = false, want true for empty query; content = %q", res.Content)
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	tl := websearch.New()
	_, err := tl.Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{})
	if err == nil {
		t.Fatal("Execute() returned nil hard error, want error for invalid JSON")
	}
}

func TestExecuteMaxResultsDefault(t *testing.T) {
	m := &mockSearcher{}
	tl := websearch.New(websearch.WithSearcher(m))
	if _, err := tl.Execute(context.Background(), json.RawMessage(`{"query":"golang"}`), tool.Deps{}); err != nil {
		t.Fatalf("Execute() returned hard error: %v", err)
	}
	if m.gotMaxResults != 5 {
		t.Errorf("searcher got maxResults = %d, want default 5", m.gotMaxResults)
	}
}

// containsAll reports whether s contains every substring in subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
