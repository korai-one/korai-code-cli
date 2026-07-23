package apiclient

// korai_extended.go — the extended-request seam of KoraiClient.
//
// The Korai OpenAI-compatible surfaces (the orchestrator's
// /v1/chat/completions and the worker's loopback HTTP endpoint) accept an
// extended sampling + constrained-decoding vocabulary — grammar, json_schema,
// seed, top_k, min_p, repeat_penalty, frequency_penalty, presence_penalty —
// that korai-sdk-go v0.4.0's ChatRequest does not model yet. Bumping the SDK
// is not an option (approved-deps set), so requests that carry any of those
// fields go through the SDK's exported DoRaw escape hatch with a body struct
// that embeds korai.ChatRequest and adds the extra wire fields. The SDK still
// owns auth headers, base URL, retry transport, and the response envelope;
// requests without extended fields keep using ChatComplete unchanged.
//
// TODO(sdk): fold these fields into korai-sdk-go's ChatRequest at the next
// SDK bump and delete this seam.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	korai "github.com/korai-one/korai-sdk-go"
)

// extendedChatRequest is the /v1/chat/completions body with the extended
// fields the Korai surfaces accept. JSON names and pointer semantics (nil =
// absent, non-nil zero forwarded) mirror the server-side openAIChatRequest in
// the korai repo exactly. Temperature and TopP shadow the embedded SDK fields
// with pointer forms — encoding/json lets the shallower field win — so a
// deliberate zero survives this path.
type extendedChatRequest struct {
	korai.ChatRequest
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	Grammar          string          `json:"grammar,omitempty"`
	JSONSchema       json.RawMessage `json:"json_schema,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	TopK             *int            `json:"top_k,omitempty"`
	MinP             *float64        `json:"min_p,omitempty"`
	RepeatPenalty    *float64        `json:"repeat_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
}

// needsExtendedRequest reports whether req carries anything SDK v0.4.0's
// ChatRequest cannot express, requiring the DoRaw path. All sampling
// parameters route through it (not just the post-v0.4.0 ones) so pointer
// semantics — a deliberate temperature/top_p of zero — hold uniformly.
func needsExtendedRequest(req Request) bool {
	return !req.Sampling.isZero() || req.Grammar != "" || len(req.JSONSchema) > 0 || req.ConstrainTools
}

// chatCompleteExtended mirrors the SDK's ChatComplete semantics (buffered,
// stream forced off, model defaulting to "auto") over DoRaw, with the extended
// fields attached. When ConstrainTools is set and no explicit
// Grammar/JSONSchema is present, it generates the fence grammar over the
// request's tools here — unlike the localproto path there is no
// constrain_tools field on the HTTP wire, so the client resolves the flag.
// Toward the remote orchestrator the grammar field is currently ignored
// (unknown fields are dropped server-side), harmlessly degrading to
// prompt-only tool teaching; the worker loopback endpoint honors it.
func (c *KoraiClient) chatCompleteExtended(ctx context.Context, base korai.ChatRequest, req Request) (*korai.ChatResponse, error) {
	base.Stream = false
	if base.Model == "" {
		base.Model = "auto"
	}
	grammar := req.Grammar
	if grammar == "" && len(req.JSONSchema) == 0 && req.ConstrainTools {
		grammar = toolFenceGrammar(req.Tools)
	}
	body := extendedChatRequest{
		ChatRequest:      base,
		Temperature:      req.Sampling.Temperature,
		TopP:             req.Sampling.TopP,
		Grammar:          grammar,
		JSONSchema:       req.JSONSchema,
		Seed:             req.Sampling.Seed,
		TopK:             req.Sampling.TopK,
		MinP:             req.Sampling.MinP,
		RepeatPenalty:    req.Sampling.RepeatPenalty,
		FrequencyPenalty: req.Sampling.FrequencyPenalty,
		PresencePenalty:  req.Sampling.PresencePenalty,
	}

	resp, err := c.inner.DoRaw(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("korai: extended chat complete: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("korai: read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, apiErrorFromBody(resp.StatusCode, raw)
	}
	var out korai.ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("korai: decode response: %w", err)
	}
	return &out, nil
}

// apiErrorFromBody maps a non-2xx response to an error, extracting the
// orchestrator's {"error":{"message","type"}} envelope when present. DoRaw
// deliberately does not map status codes, so this path does its own.
func apiErrorFromBody(status int, raw []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Error.Message != "" {
		if envelope.Error.Type != "" {
			return fmt.Errorf("korai: api error (HTTP %d, %s): %s", status, envelope.Error.Type, envelope.Error.Message)
		}
		return fmt.Errorf("korai: api error (HTTP %d): %s", status, envelope.Error.Message)
	}
	body := string(raw)
	const maxBody = 200
	if len(body) > maxBody {
		body = body[:maxBody] + "…"
	}
	return fmt.Errorf("korai: api error: HTTP %d: %s", status, body)
}
