// Package session bridges the CLI's backend-agnostic conversation model
// (apiclient.Message + ContentBlock) to the rich, block-based canonical session
// type from the Korai SDK (korai.Session / korai.SessionMessage), and mints
// session ids. Persistence and cross-device sync themselves live in the shared
// SDK packages (github.com/korai-one/korai-sdk-go/session and .../session/
// synchub); this package only owns the map-UP/map-DOWN adapter and NewID, so the
// CLI stores conversations in the SAME canonical format every Korai surface uses
// and a session started here can teleport losslessly to cmd/kode or the
// dashboard. See docs/HISTORY_SYNC.md §14 in the sibling korai repo.
//
// The CLI's ContentBlock set is the blueprint for the canonical Block set, so the
// mapping is 1:1 and lossless in both directions for CLI-produced data (asserted
// by the round-trip test).
package session

import (
	"encoding/json"

	korai "github.com/korai-one/korai-sdk-go"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// Tool identifies conversations this CLI produces, stamped into the canonical
// korai.Session.Tool metadata so teleport can attribute a session to its origin
// surface. It is advisory metadata only, never used for routing.
const Tool = "korai-code-cli"

// ToCanonicalMessages maps a slice of the CLI's flat-per-block messages UP into
// the canonical session-message form the store and sync wire use.
func ToCanonicalMessages(msgs []apiclient.Message) []korai.SessionMessage {
	if msgs == nil {
		return nil
	}
	out := make([]korai.SessionMessage, len(msgs))
	for i, m := range msgs {
		out[i] = ToCanonicalMessage(m)
	}
	return out
}

// FromCanonicalMessages maps canonical session messages back DOWN into the CLI's
// apiclient.Message form. It is the exact inverse of ToCanonicalMessages for
// CLI-produced data.
func FromCanonicalMessages(msgs []korai.SessionMessage) []apiclient.Message {
	if msgs == nil {
		return nil
	}
	out := make([]apiclient.Message, len(msgs))
	for i, m := range msgs {
		out[i] = FromCanonicalMessage(m)
	}
	return out
}

// ToCanonicalMessage maps one apiclient.Message UP into a korai.SessionMessage,
// preserving block order. The CLI's ContentBlock variants map 1:1 onto the
// canonical Block variants.
func ToCanonicalMessage(m apiclient.Message) korai.SessionMessage {
	blocks := make([]korai.Block, 0, len(m.Content))
	for _, b := range m.Content {
		if cb := toCanonicalBlock(b); cb != nil {
			blocks = append(blocks, cb)
		}
	}
	return korai.SessionMessage{Role: string(m.Role), Blocks: blocks}
}

// FromCanonicalMessage maps one korai.SessionMessage back DOWN into an
// apiclient.Message, preserving block order.
func FromCanonicalMessage(m korai.SessionMessage) apiclient.Message {
	content := make([]apiclient.ContentBlock, 0, len(m.Blocks))
	for _, b := range m.Blocks {
		if cb := fromCanonicalBlock(b); cb != nil {
			content = append(content, cb)
		}
	}
	return apiclient.Message{Role: apiclient.Role(m.Role), Content: content}
}

// toCanonicalBlock maps one CLI content block UP into its canonical variant. The
// tool-call input JSON is passed through byte-for-byte so it round-trips exactly.
func toCanonicalBlock(b apiclient.ContentBlock) korai.Block {
	switch v := b.(type) {
	case apiclient.TextBlock:
		return korai.TextBlock{Text: v.Text}
	case apiclient.ToolCallBlock:
		return korai.ToolUseBlock{ID: v.ID, Name: v.Name, Input: cloneRaw(v.Input)}
	case apiclient.ToolResultBlock:
		// The canonical block carries an optional Name (for a flat role="tool"
		// producer); the CLI has none, so it stays empty and round-trips.
		return korai.ToolResultBlock{ToolCallID: v.ToolCallID, Content: v.Content, IsError: v.IsError}
	case apiclient.ImageBlock:
		return korai.ImageBlock{Source: v.Source}
	default:
		return nil
	}
}

// fromCanonicalBlock maps one canonical block back DOWN into a CLI content block.
// A canonical ToolResultBlock.Name is dropped (the CLI's ToolResultBlock has no
// Name field); this is lossless for CLI-produced data, where Name is always
// empty.
func fromCanonicalBlock(b korai.Block) apiclient.ContentBlock {
	switch v := b.(type) {
	case korai.TextBlock:
		return apiclient.TextBlock{Text: v.Text}
	case korai.ToolUseBlock:
		return apiclient.ToolCallBlock{ID: v.ID, Name: v.Name, Input: cloneRaw(v.Input)}
	case korai.ToolResultBlock:
		return apiclient.ToolResultBlock{ToolCallID: v.ToolCallID, Content: v.Content, IsError: v.IsError}
	case korai.ImageBlock:
		return apiclient.ImageBlock{Source: v.Source}
	default:
		return nil
	}
}

// cloneRaw returns a copy of raw so the mapped block does not alias the source's
// backing array. Nil in, nil out.
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}
