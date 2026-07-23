package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ptr returns a pointer to v, for building Sampling literals in tests.
func ptr[T any](v T) *T { return &v }

// captureServer records the JSON body of each /v1/chat/completions request and
// replies with a minimal valid completion.
func captureServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		bodies = append(bodies, m)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

// TestKoraiExtendedSamplingOnWire verifies the DoRaw path: extended sampling
// fields reach the wire under the exact JSON names the Korai surfaces accept,
// and a deliberate zero survives (pointer semantics).
func TestKoraiExtendedSamplingOnWire(t *testing.T) {
	t.Parallel()

	srv, bodies := captureServer(t)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	req := Request{
		Messages: []Message{userMsg("hi")},
		Sampling: Sampling{
			Temperature:      ptr(0.0), // deliberate zero must survive
			TopP:             ptr(0.9),
			TopK:             ptr(40),
			MinP:             ptr(0.05),
			Seed:             ptr(0), // deliberate zero must survive
			RepeatPenalty:    ptr(1.1),
			FrequencyPenalty: ptr(0.25),
			PresencePenalty:  ptr(0.5),
		},
		Grammar: "root ::= \"yes\"",
	}
	collect(t, mustComplete(t, c, req))

	if len(*bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(*bodies))
	}
	body := (*bodies)[0]

	want := map[string]any{
		"temperature":       0.0,
		"top_p":             0.9,
		"top_k":             40.0,
		"min_p":             0.05,
		"seed":              0.0,
		"repeat_penalty":    1.1,
		"frequency_penalty": 0.25,
		"presence_penalty":  0.5,
		"grammar":           "root ::= \"yes\"",
	}
	for key, val := range want {
		got, ok := body[key]
		if !ok {
			t.Errorf("wire body missing %q", key)
			continue
		}
		if got != val {
			t.Errorf("wire %q = %v, want %v", key, got, val)
		}
	}
	if body["stream"] != nil && body["stream"] != false {
		t.Errorf("stream = %v, want false/absent", body["stream"])
	}
}

// TestKoraiPlainPathOmitsExtendedFields verifies a request without extended
// fields keeps using the SDK path and sends none of the extended keys.
func TestKoraiPlainPathOmitsExtendedFields(t *testing.T) {
	t.Parallel()

	srv, bodies := captureServer(t)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	collect(t, mustComplete(t, c, Request{Messages: []Message{userMsg("hi")}}))

	if len(*bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(*bodies))
	}
	body := (*bodies)[0]
	for _, key := range []string{"grammar", "json_schema", "seed", "top_k", "min_p", "repeat_penalty", "frequency_penalty", "presence_penalty", "temperature", "top_p"} {
		if _, ok := body[key]; ok {
			t.Errorf("plain request must not carry %q (got %v)", key, body[key])
		}
	}
}

// TestKoraiConstrainToolsGeneratesGrammar verifies ConstrainTools resolves to
// a generated fence grammar over the request's tools on the HTTP wire (there
// is no constrain_tools field on this path).
func TestKoraiConstrainToolsGeneratesGrammar(t *testing.T) {
	t.Parallel()

	srv, bodies := captureServer(t)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	req := Request{
		Messages:       []Message{userMsg("hi")},
		Tools:          []ToolDef{{Name: "Read"}, {Name: "Bash"}},
		ConstrainTools: true,
	}
	collect(t, mustComplete(t, c, req))

	body := (*bodies)[0]
	grammar, _ := body["grammar"].(string)
	if !strings.Contains(grammar, `toolname ::= "Bash" | "Read"`) {
		t.Errorf("generated grammar missing tool alternation, got:\n%s", grammar)
	}
	if _, ok := body["constrain_tools"]; ok {
		t.Error("constrain_tools must not leak onto the HTTP wire")
	}
}

// TestKoraiExplicitGrammarWinsOverConstrainTools verifies an explicit Grammar
// is forwarded untouched even when ConstrainTools is also set.
func TestKoraiExplicitGrammarWinsOverConstrainTools(t *testing.T) {
	t.Parallel()

	srv, bodies := captureServer(t)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	req := Request{
		Messages:       []Message{userMsg("hi")},
		Tools:          []ToolDef{{Name: "Read"}},
		Grammar:        "root ::= \"no\"",
		ConstrainTools: true,
	}
	collect(t, mustComplete(t, c, req))

	if got := (*bodies)[0]["grammar"]; got != "root ::= \"no\"" {
		t.Errorf("grammar = %v, want the explicit grammar", got)
	}
}

// TestKoraiJSONSchemaOnWire verifies JSONSchema forwards raw, and that
// ConstrainTools does not overwrite it with a fence grammar.
func TestKoraiJSONSchemaOnWire(t *testing.T) {
	t.Parallel()

	srv, bodies := captureServer(t)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	req := Request{
		Messages:       []Message{userMsg("hi")},
		Tools:          []ToolDef{{Name: "Read"}},
		JSONSchema:     json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`),
		ConstrainTools: true,
	}
	collect(t, mustComplete(t, c, req))

	body := (*bodies)[0]
	if _, ok := body["grammar"]; ok {
		t.Error("no grammar should be generated when an explicit JSONSchema is present")
	}
	schema, ok := body["json_schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Errorf("json_schema = %v, want the raw schema object", body["json_schema"])
	}
}

// TestKoraiExtendedAPIError verifies the extended path maps the orchestrator
// error envelope to a descriptive error.
func TestKoraiExtendedAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad grammar","type":"invalid_request_error"}}`))
	}))
	t.Cleanup(srv.Close)
	c := NewKoraiClient("sk-test", srv.URL, "auto")

	ch, err := c.Complete(context.Background(), Request{
		Messages: []Message{userMsg("hi")},
		Grammar:  "root ::= broken",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var errEvt *ErrorEvent
	for _, e := range collect(t, ch) {
		if ee, ok := e.(ErrorEvent); ok {
			errEvt = &ee
		}
	}
	if errEvt == nil {
		t.Fatal("expected an ErrorEvent")
	}
	msg := errEvt.Err.Error()
	if !strings.Contains(msg, "400") || !strings.Contains(msg, "bad grammar") || !strings.Contains(msg, "invalid_request_error") {
		t.Errorf("error = %q, want status, type and message surfaced", msg)
	}
}
