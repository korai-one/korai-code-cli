package apiclient

import "context"

// Client is the inference boundary. engine calls this; nothing below it
// knows which network backend is in use.
//
// AnthropicClient implements Client today (all call sites annotated
// // TODO KORAI SDK). KoraiClient will implement the same interface against
// the Korai P2P SDK. A StranglerClient can wrap both for gradual migration.
type Client interface {
	// Complete sends req to the inference backend and returns a channel of
	// streaming events. The channel is closed when the stream ends or ctx is
	// cancelled. Callers must drain the channel to release resources.
	Complete(ctx context.Context, req Request) (<-chan Event, error)
}
