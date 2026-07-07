package main

import (
	"encoding/json"
	"fmt"

	"github.com/Nevaero/korai-code-cli/internal/engine"
)

// Output format identifiers for the headless --print mode. "text" streams the
// human-readable output; "json" emits one JSON object per line (JSONL).
const (
	outputFormatText = "text"
	outputFormatJSON = "json"
)

// validateOutputFormat returns an error if s is not a recognized --output-format
// value. It is used to reject unknown formats before a run starts.
func validateOutputFormat(s string) error {
	switch s {
	case outputFormatText, outputFormatJSON:
		return nil
	default:
		return fmt.Errorf("invalid --output-format %q: want %q or %q", s, outputFormatText, outputFormatJSON)
	}
}

// encodeEvent converts a single engine event into a compact, single-line JSON
// object for the headless JSONL output. The schema is stable: every object has a
// "type" field discriminating the shape, plus the fields relevant to that event.
// It is pure (no I/O) so it can be unit-tested without running the engine.
func encodeEvent(evt engine.Event) ([]byte, error) {
	switch v := evt.(type) {
	case engine.TextEvent:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{outputFormatText, v.Text})
	case engine.ToolStartEvent:
		input := v.Input
		if len(input) == 0 {
			input = json.RawMessage("null")
		}
		return json.Marshal(struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{"tool_start", v.Name, input})
	case engine.ToolResultEvent:
		return json.Marshal(struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		}{"tool_result", v.Name, v.Result.Content, v.Result.IsError})
	case engine.CompactedEvent:
		return json.Marshal(struct {
			Type   string `json:"type"`
			Before int    `json:"before"`
			After  int    `json:"after"`
		}{"compacted", v.Before, v.After})
	case engine.DoneEvent:
		return json.Marshal(struct {
			Type     string `json:"type"`
			Messages int    `json:"messages"`
		}{"done", len(v.Messages)})
	case engine.ErrorEvent:
		msg := ""
		if v.Err != nil {
			msg = v.Err.Error()
		}
		return json.Marshal(struct {
			Type  string `json:"type"`
			Error string `json:"error"`
		}{"error", msg})
	default:
		return nil, fmt.Errorf("encodeEvent: unknown event type %T", evt)
	}
}
