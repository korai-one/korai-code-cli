package session

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// formatVersion is the on-disk JSONL schema version, recorded in the header so
// future format changes can be detected.
const formatVersion = 1

// entry kinds, used as the first field of every JSONL line.
const (
	kindHeader  = "header"
	kindMessage = "message"
)

// headerDTO is the first line of a session file: metadata written once when the
// file is created. It records the codec name (Enc) so Load can decode message
// lines, and is always stored in the clear.
type headerDTO struct {
	Kind    string    `json:"kind"` // always kindHeader
	Version int       `json:"version"`
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	CWD     string    `json:"cwd"`
	Model   string    `json:"model"`
	Enc     string    `json:"enc"` // codec name; "none" = plaintext
}

// messageDTO is one appended conversation message. Content uses blockDTO so the
// apiclient.ContentBlock interface round-trips with an explicit type tag.
type messageDTO struct {
	Kind    string     `json:"kind"` // always kindMessage
	Role    string     `json:"role"`
	Content []blockDTO `json:"content"`
}

// kindPeek reads just the discriminator field of a JSONL line.
type kindPeek struct {
	Kind string `json:"kind"`
}

type blockDTO struct {
	Kind       string          `json:"kind"` // text | tool_call | tool_result | image
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Content    string          `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Source     string          `json:"source,omitempty"` // image data URI or URL
}

func headerFromRecord(r Record, enc string) headerDTO {
	return headerDTO{
		Kind: kindHeader, Version: formatVersion,
		ID: r.ID, Created: r.Created, CWD: r.CWD, Model: r.Model, Enc: enc,
	}
}

func msgToDTO(m apiclient.Message) messageDTO {
	blocks := make([]blockDTO, 0, len(m.Content))
	for _, b := range m.Content {
		blocks = append(blocks, blockToDTO(b))
	}
	return messageDTO{Kind: kindMessage, Role: string(m.Role), Content: blocks}
}

func blockToDTO(b apiclient.ContentBlock) blockDTO {
	switch v := b.(type) {
	case apiclient.TextBlock:
		return blockDTO{Kind: "text", Text: v.Text}
	case apiclient.ToolCallBlock:
		return blockDTO{Kind: "tool_call", ID: v.ID, Name: v.Name, Input: v.Input}
	case apiclient.ToolResultBlock:
		return blockDTO{Kind: "tool_result", ToolCallID: v.ToolCallID, Content: v.Content, IsError: v.IsError}
	case apiclient.ImageBlock:
		return blockDTO{Kind: "image", Source: v.Source}
	default:
		return blockDTO{Kind: "text"}
	}
}

func msgFromDTO(m messageDTO) apiclient.Message {
	blocks := make([]apiclient.ContentBlock, 0, len(m.Content))
	for _, b := range m.Content {
		if cb := blockFromDTO(b); cb != nil {
			blocks = append(blocks, cb)
		}
	}
	return apiclient.Message{Role: apiclient.Role(m.Role), Content: blocks}
}

// compactJSON returns raw with insignificant whitespace removed. If raw is empty
// or invalid it is returned unchanged. Defensive: keeps tool-call input compact
// so it round-trips byte-for-byte with what the model produced.
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
		return apiclient.ToolCallBlock{ID: b.ID, Name: b.Name, Input: compactJSON(b.Input)}
	case "tool_result":
		return apiclient.ToolResultBlock{ToolCallID: b.ToolCallID, Content: b.Content, IsError: b.IsError}
	case "image":
		return apiclient.ImageBlock{Source: b.Source}
	default:
		return nil
	}
}
