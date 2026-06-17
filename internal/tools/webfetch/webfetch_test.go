package webfetch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/tools/webfetch"
)

// rawURL marshals a URL into the tool's JSON input shape.
func rawURL(t *testing.T, u string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(webfetch.Input{URL: u})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return raw
}

func TestExecutePlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	tl := webfetch.New(webfetch.WithHTTPClient(srv.Client()))
	res, err := tl.Execute(context.Background(), rawURL(t, srv.URL), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected soft error: %q", res.Content)
	}
	if diff := cmp.Diff("hello world", res.Content); diff != "" {
		t.Errorf("content mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteHTMLStripped(t *testing.T) {
	const doc = `<html><head><style>.x{color:red}</style>` +
		`<script>var a = 1 < 2;</script></head>` +
		`<body><h1>Title</h1><p>Hello&nbsp;&amp; welcome</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	}))
	defer srv.Close()

	tl := webfetch.New(webfetch.WithHTTPClient(srv.Client()))
	res, err := tl.Execute(context.Background(), rawURL(t, srv.URL), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected soft error: %q", res.Content)
	}
	//   is the unescaped &nbsp;.
	want := "TitleHello & welcome"
	if diff := cmp.Diff(want, res.Content); diff != "" {
		t.Errorf("content mismatch (-want +got):\n%s", diff)
	}
}

func TestExecuteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	tl := webfetch.New(webfetch.WithHTTPClient(srv.Client()))
	res, err := tl.Execute(context.Background(), rawURL(t, srv.URL), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected soft error for 404, got content: %q", res.Content)
	}
}

func TestExecuteNonHTTPScheme(t *testing.T) {
	tl := webfetch.New()
	res, err := tl.Execute(context.Background(), rawURL(t, "ftp://example.com/x"), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected soft error for ftp scheme, got content: %q", res.Content)
	}
}

func TestExecuteEmptyURL(t *testing.T) {
	tl := webfetch.New()
	res, err := tl.Execute(context.Background(), rawURL(t, ""), tool.Deps{})
	if err != nil {
		t.Fatalf("Execute returned hard error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected soft error for empty URL, got content: %q", res.Content)
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	tl := webfetch.New()
	_, err := tl.Execute(context.Background(), json.RawMessage(`{not json`), tool.Deps{})
	if err == nil {
		t.Fatal("expected hard error for invalid JSON, got nil")
	}
}

func TestExecuteContextCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until the client gives up
		close(block)
	}))
	defer srv.Close()

	tl := webfetch.New(webfetch.WithHTTPClient(srv.Client()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request even starts

	res, err := tl.Execute(ctx, rawURL(t, srv.URL), tool.Deps{})
	if err == nil && !res.IsError {
		t.Fatalf("expected error or soft error on cancelled context, got content: %q", res.Content)
	}
}

func TestDeclarations(t *testing.T) {
	tl := webfetch.New()
	if got := tl.Name(); got != "WebFetch" {
		t.Errorf("Name() = %q, want %q", got, "WebFetch")
	}
	if !tl.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	if !tl.ConcurrencySafe() {
		t.Error("ConcurrencySafe() = false, want true")
	}
}

func TestCheckPermission(t *testing.T) {
	tl := webfetch.New()
	raw := rawURL(t, "https://example.com")

	tests := []struct {
		name string
		mode perm.Mode
		want perm.Decision
	}{
		{"default asks", perm.ModeDefault, perm.DecisionAsk},
		{"plan asks", perm.ModePlan, perm.DecisionAsk},
		{"acceptEdits asks", perm.ModeAcceptEdits, perm.DecisionAsk},
		{"bypass allows", perm.ModeBypassPermissions, perm.DecisionAllow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tl.CheckPermission(context.Background(), raw, tt.mode); got != tt.want {
				t.Errorf("CheckPermission(%v) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

// compile-time guard: webfetch.Tool satisfies tool.Tool.
var _ tool.Tool = (*webfetch.Tool)(nil)
