package apiclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// modelsServer serves a canned GET /v1/models body.
func modelsServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// orchestratorModels mirrors the orchestrator's /v1/models shape: aliases
// without context_len, canonical rows with it.
const orchestratorModels = `{"object":"list","data":[
	{"id":"auto","object":"model","kind":"alias"},
	{"id":"gemma-4-31b-thinking-4bit","object":"model","kind":"canonical","context_len":40000},
	{"id":"small-8b-4bit","object":"model","kind":"canonical","context_len":16000}
]}`

// loopbackModels mirrors the worker loopback endpoint's /v1/models shape: no
// context_len anywhere.
const loopbackModels = `{"object":"list","data":[
	{"id":"auto","object":"model","kind":"alias"},
	{"id":"gemma-4-31b-thinking-4bit","object":"model","kind":"canonical"}
]}`

func TestKoraiClientContextLen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		body  string
		model string
		want  int
	}{
		{"exact canonical match", orchestratorModels, "gemma-4-31b-thinking-4bit", 40000},
		// An alias can route to any canonical model, so the smallest
		// advertised window is the conservative answer.
		{"alias falls back to smallest", orchestratorModels, "auto", 16000},
		{"unknown id falls back to smallest", orchestratorModels, "nope", 16000},
		// The worker loopback endpoint advertises no context_len: unknown.
		{"loopback yields unknown", loopbackModels, "auto", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := modelsServer(t, tc.body)
			c := apiclient.NewKoraiClient("key", srv.URL, tc.model)
			if got := c.ContextLen(context.Background(), tc.model); got != tc.want {
				t.Errorf("ContextLen(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestKoraiClientContextLenUnreachable(t *testing.T) {
	t.Parallel()
	// A dead endpoint yields 0 (unknown), never an error or a panic.
	c := apiclient.NewKoraiClient("key", "http://127.0.0.1:1", "auto")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // don't wait on retries — a cancelled ctx forces the fast failure path
	if got := c.ContextLen(ctx, "auto"); got != 0 {
		t.Errorf("ContextLen on unreachable endpoint = %d, want 0", got)
	}
}

func TestClientSelectorForwardsContextLen(t *testing.T) {
	t.Parallel()

	srv := modelsServer(t, orchestratorModels)
	remote := apiclient.NewKoraiClient("key", srv.URL, "auto")
	local := apiclient.NewLocalWorkerClient("/tmp/sock", "auto") // no ContextSizer

	sel := apiclient.NewClientSelector(apiclient.WorkerLocal, local, remote)
	if got := sel.ContextLen(context.Background(), "auto"); got != 0 {
		t.Errorf("local mode ContextLen = %d, want 0 (direct channel has no context info)", got)
	}
	if err := sel.SetMode(string(apiclient.WorkerRemote)); err != nil {
		t.Fatal(err)
	}
	if got := sel.ContextLen(context.Background(), "auto"); got != 16000 {
		t.Errorf("remote mode ContextLen = %d, want 16000", got)
	}
}
