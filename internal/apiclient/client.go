package apiclient

import "context"

// Client is the inference boundary. engine calls this; nothing below it
// knows which network backend is in use.
//
// KoraiClient implements Client against the Korai P2P inference SDK;
// LocalWorkerClient implements it against a co-located or LAN worker. A
// ClientSelector can wrap both so the active backend is switchable at runtime.
// No korai.* type crosses this boundary; everything is converted to apiclient's
// own types.
type Client interface {
	// Complete sends req to the inference backend and returns a channel of
	// streaming events. The channel is closed when the stream ends or ctx is
	// cancelled. Callers must drain the channel to release resources.
	Complete(ctx context.Context, req Request) (<-chan Event, error)
}
