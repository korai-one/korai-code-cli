// Package webfetch implements the WebFetch tool: a read-only tool that fetches
// the contents of an HTTP(S) URL and returns it as plain text.
//
// Name mapping: the conceptual WebFetchTool becomes package webfetch, type
// webfetch.Tool, constructor webfetch.New.
//
// The tool's *http.Client is injected via the New(...) options rather than the
// frozen tool.Deps, which keeps tests fully hermetic: a test can point the tool
// at an httptest.Server with no real network access.
package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// userAgent is the User-Agent header sent with every WebFetch request.
const userAgent = "korai-code-cli"

// maxBodyBytes caps the number of response bytes read into memory (5 MB),
// guarding against unbounded or hostile responses.
const maxBodyBytes = 5 << 20

// defaultTimeout is the request timeout for the default HTTP client.
const defaultTimeout = 30 * time.Second

// tagRE matches any HTML tag (e.g. "<p>", "</div>", "<br/>").
var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)

// scriptStyleRE matches entire <script>...</script> and <style>...</style>
// blocks, including their contents, case-insensitively.
var scriptStyleRE = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</\s*(script|style)\s*>`)

// Input is the structured input for the WebFetch tool.
type Input struct {
	// URL is the http or https URL to fetch.
	URL string `json:"url" jsonschema:"required,description=The URL to fetch (http or https)"`
}

// Tool implements tool.Tool for fetching URLs over HTTP(S).
type Tool struct {
	client *http.Client
}

// Option configures a Tool at construction time.
type Option func(*Tool)

// WithHTTPClient sets the *http.Client the tool uses for requests. This is the
// injection point that lets tests substitute an httptest.Server client and stay
// hermetic. A nil client is ignored, leaving the default in place.
func WithHTTPClient(c *http.Client) Option {
	return func(t *Tool) {
		if c != nil {
			t.client = c
		}
	}
}

// New returns a new WebFetch tool. By default it uses an *http.Client with a
// 30s timeout; pass WithHTTPClient to override it (e.g. in tests).
func New(opts ...Option) *Tool {
	t := &Tool{client: &http.Client{Timeout: defaultTimeout}}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Name returns "WebFetch".
func (t *Tool) Name() string { return "WebFetch" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Fetches the contents of a URL and returns it as text."
}

// InputSchema returns the JSON schema for WebFetch's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — WebFetch never mutates local state.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — independent fetches are safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission gates network egress: bypass mode allows the fetch, every
// other mode asks the user first.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	if mode == perm.ModeBypassPermissions {
		return perm.DecisionAllow
	}
	return perm.DecisionAsk
}

// Execute fetches Input.URL and returns its text content. Transport failures,
// non-2xx statuses, empty URLs, and non-http(s) schemes are returned as soft
// errors (Result.IsError); only malformed JSON input is a hard error.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("WebFetch: invalid input: %w", err)
	}
	if in.URL == "" {
		return tool.Result{Content: "url is required", IsError: true}, nil
	}

	u, err := url.Parse(in.URL)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("invalid URL %q: %v", in.URL, err),
			IsError: true,
		}, nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return tool.Result{
			Content: fmt.Sprintf("unsupported URL scheme %q: only http and https are allowed", u.Scheme),
			IsError: true,
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot build request for %s: %v", in.URL, err),
			IsError: true,
		}, nil
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot fetch %s: %v", in.URL, err),
			IsError: true,
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tool.Result{
			Content: fmt.Sprintf("fetching %s returned status %s", in.URL, resp.Status),
			IsError: true,
		}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot read body of %s: %v", in.URL, err),
			IsError: true,
		}, nil
	}

	text := string(body)
	if isHTML(resp.Header.Get("Content-Type")) {
		text = htmlToText(text)
	}
	return tool.Result{Content: text}, nil
}

// isHTML reports whether a Content-Type header value denotes HTML.
func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "html")
}

// htmlToText reduces an HTML document to plain text using a deliberately simple
// stdlib approach: drop <script>/<style> blocks, strip remaining tags, then
// unescape entities. It is not a full HTML parser — just enough to give the
// model readable text.
func htmlToText(s string) string {
	s = scriptStyleRE.ReplaceAllString(s, "")
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}
