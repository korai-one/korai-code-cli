// Package wsevent bridges the engine's event channel onto the WebSocket wire
// protocol. It is the server→client half of `korai serve`: it consumes
// engine.Event values and forwards each as a proto.ServerEvent through a send
// function, returning the post-turn conversation history so the caller can
// carry context into the next turn.
//
// Tool-call correlation: engine.Event does not carry a tool-call id, but the
// engine executes tool calls strictly serially — each ToolStartEvent is
// immediately followed by its own ToolResultEvent before any next start, and a
// tool blocked before execution (unknown, denied, or hook-vetoed) emits a
// result with no preceding start. The bridge therefore mints an id at each
// start and reuses it for the very next result, minting a fresh id for a
// result that has no pending start. This keeps the engine contract untouched
// while giving the client stable start↔result pairing.
package wsevent

import (
	"github.com/google/uuid"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/proto"
)

// Bridge forwards every event from events to the client via send until the
// channel closes. It returns the conversation history captured from the turn's
// DoneEvent (nil if the turn ended on an error, which carries no history) and
// the first send error encountered, if any. A send error stops forwarding: the
// connection is presumed broken, so there is no point draining the rest.
func Bridge(events <-chan engine.Event, send func(proto.ServerEvent) error) ([]apiclient.Message, error) {
	var (
		history   []apiclient.Message
		lastStart string // id of a ToolStartEvent awaiting its ToolResultEvent
	)
	for evt := range events {
		var out proto.ServerEvent
		switch v := evt.(type) {
		case engine.TextEvent:
			out = proto.Text(v.Text)
		case engine.ToolStartEvent:
			lastStart = uuid.NewString()
			out = proto.ToolStart(lastStart, v.Name, v.Input)
		case engine.ToolResultEvent:
			id := lastStart
			if id == "" {
				id = uuid.NewString() // a result with no start (blocked before execution)
			}
			lastStart = ""
			out = proto.ToolResult(id, v.Name, v.Result.Content, v.Result.IsError)
		case engine.CompactedEvent:
			out = proto.Compact(v.Before, v.After)
		case engine.ErrorEvent:
			out = proto.Error(v.Err.Error())
		case engine.DoneEvent:
			history = v.Messages
			out = proto.Done()
		default:
			continue // unknown event types are skipped, not forwarded
		}
		if err := send(out); err != nil {
			return history, err
		}
	}
	return history, nil
}
