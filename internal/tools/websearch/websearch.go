// Package websearch implements the WebSearch tool: a read-only tool that
// searches the web for a query and returns a list of result titles and URLs.
//
// There is no approved search provider wired in yet, so the tool is structured
// around an injectable Searcher backend. The default backend returns
// ErrNotConfigured; a real backend (likely the Korai network) is injected later
// via WithSearcher. This keeps the tool present, type-safe, and testable without
// committing to a provider.
//
// Name mapping: the conceptual "WebSearchTool" becomes package websearch,
// constructor websearch.New, type websearch.Tool.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// ErrNotConfigured is returned by the default Searcher backend to signal that no
// web search provider has been wired in. Callers branch on it with errors.Is.
var ErrNotConfigured = errors.New("web search backend not configured")

// Result is a single web search hit returned by a Searcher.
type Result struct {
	// Title is the human-readable title of the result.
	Title string
	// URL is the address of the result.
	URL string
	// Snippet is an optional short excerpt describing the result.
	Snippet string
}

// Searcher is the injectable backend that performs the actual web search.
// Implementations must honor ctx cancellation and must not print to the screen.
type Searcher interface {
	// Search runs the query and returns up to maxResults hits.
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}

// notConfiguredSearcher is the default backend; its Search always returns
// ErrNotConfigured because no provider has been injected.
type notConfiguredSearcher struct{}

// Search reports that no backend is configured.
func (notConfiguredSearcher) Search(_ context.Context, _ string, _ int) ([]Result, error) {
	return nil, ErrNotConfigured
}

// Input is the structured input for the WebSearch tool.
type Input struct {
	// Query is the search query.
	Query string `json:"query" jsonschema:"required,description=the search query"`
	// MaxResults is the maximum number of results to return (default 5).
	MaxResults int `json:"max_results,omitempty" jsonschema:"description=maximum number of results, default 5"`
}

// defaultMaxResults is applied when Input.MaxResults is 0.
const defaultMaxResults = 5

// Tool implements tool.Tool for searching the web.
type Tool struct {
	searcher Searcher
}

// Option configures a Tool at construction time.
type Option func(*Tool)

// WithSearcher injects the backend used to perform searches, replacing the
// default not-configured backend.
func WithSearcher(s Searcher) Option {
	return func(t *Tool) { t.searcher = s }
}

// New returns a new WebSearch tool. By default it uses a backend that reports
// ErrNotConfigured; inject a real backend with WithSearcher.
func New(opts ...Option) *Tool {
	t := &Tool{searcher: notConfiguredSearcher{}}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Name returns "WebSearch".
func (t *Tool) Name() string { return "WebSearch" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Searches the web for a query and returns a list of result titles and URLs."
}

// InputSchema returns the JSON schema for WebSearch's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — WebSearch never mutates local state.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — searches are safe to run in parallel.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission asks before searching in every mode except
// ModeBypassPermissions, because a search performs network egress.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	if mode == perm.ModeBypassPermissions {
		return perm.DecisionAllow
	}
	return perm.DecisionAsk
}

// Execute validates the input, runs the injected Searcher, and formats the
// results as text. Validation/backend failures surface as soft errors
// (Result.IsError); only malformed JSON is a hard error.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("WebSearch: invalid input: %w", err)
	}
	if in.Query == "" {
		return tool.Result{Content: "query is required", IsError: true}, nil
	}

	maxResults := in.MaxResults
	if maxResults == 0 {
		maxResults = defaultMaxResults
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	results, err := t.searcher.Search(ctx, in.Query, maxResults)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return tool.Result{
				Content: "web search is not available: no search backend is configured. " +
					"A search provider must be wired in before WebSearch can be used.",
				IsError: true,
			}, nil
		}
		return tool.Result{
			Content: fmt.Sprintf("web search failed: %v", err),
			IsError: true,
		}, nil
	}

	return tool.Result{Content: formatResults(in.Query, results)}, nil
}

// formatResults renders search results as text, one per line, in the form
// "Title — URL" with an optional indented snippet line.
func formatResults(query string, results []Result) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for %q.", query)
	}

	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s — %s", r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "\n    %s", r.Snippet)
		}
	}
	return b.String()
}
