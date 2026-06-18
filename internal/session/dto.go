package session

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// recordDTO is the on-disk shape of a Record. Messages use blockDTO so the
// apiclient.ContentBlock interface round-trips with an explicit type tag.
type recordDTO struct {
	ID       string       `json:"id"`
	Created  time.Time    `json:"created"`
	Updated  time.Time    `json:"updated"`
	CWD      string       `json:"cwd"`
	Model    string       `json:"model"`
	Messages []messageDTO `json:"messages"`
}

type messageDTO struct {
	Role    string     `json:"role"`
	Content []blockDTO `json:"content"`
}

type blockDTO struct {
	Kind       string          `json:"kind"` // text | tool_call | tool_result
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Content    string          `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

func toDTO(r Record) recordDTO {
	msgs := make([]messageDTO, 0, len(r.Messages))
	for _, m := range r.Messages {
		blocks := make([]blockDTO, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, blockToDTO(b))
		}
		msgs = append(msgs, messageDTO{Role: string(m.Role), Content: blocks})
	}
	return recordDTO{
		ID: r.ID, Created: r.Created, Updated: r.Updated,
		CWD: r.CWD, Model: r.Model, Messages: msgs,
	}
}

func blockToDTO(b apiclient.ContentBlock) blockDTO {
	switch v := b.(type) {
	case apiclient.TextBlock:
		return blockDTO{Kind: "text", Text: v.Text}
	case apiclient.ToolCallBlock:
		return blockDTO{Kind: "tool_call", ID: v.ID, Name: v.Name, Input: v.Input}
	case apiclient.ToolResultBlock:
		return blockDTO{Kind: "tool_result", ToolCallID: v.ToolCallID, Content: v.Content, IsError: v.IsError}
	default:
		return blockDTO{Kind: "text"}
	}
}

func fromDTO(d recordDTO) Record {
	msgs := make([]apiclient.Message, 0, len(d.Messages))
	for _, m := range d.Messages {
		blocks := make([]apiclient.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if cb := blockFromDTO(b); cb != nil {
				blocks = append(blocks, cb)
			}
		}
		msgs = append(msgs, apiclient.Message{Role: apiclient.Role(m.Role), Content: blocks})
	}
	return Record{
		ID: d.ID, Created: d.Created, Updated: d.Updated,
		CWD: d.CWD, Model: d.Model, Messages: msgs,
	}
}

// compactJSON returns raw with insignificant whitespace removed. If raw is empty
// or invalid it is returned unchanged.
func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}

func blockFromDTO(b blockDTO) apiclient.ContentBlock {
	switch b.Kind {
	case "text":
		return apiclient.TextBlock{Text: b.Text}
	case "tool_call":
		// MarshalIndent re-indents embedded RawMessage; compact it back so the
		// input round-trips byte-for-byte with what the model produced.
		return apiclient.ToolCallBlock{ID: b.ID, Name: b.Name, Input: compactJSON(b.Input)}
	case "tool_result":
		return apiclient.ToolResultBlock{ToolCallID: b.ToolCallID, Content: b.Content, IsError: b.IsError}
	default:
		return nil
	}
}
