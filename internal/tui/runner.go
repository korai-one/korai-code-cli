package tui

import (
	"context"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
)

// Runner is the slice of the engine the TUI depends on: start a turn and
// receive its events on a channel. *engine.Engine satisfies this; tests inject
// a fake so the model can be exercised without a real inference backend.
type Runner interface {
	Run(ctx context.Context, messages []apiclient.Message, system string) <-chan engine.Event
	// Enqueue injects user text into the in-flight turn (mid-turn steering),
	// folded into history at the next tool-loop iteration.
	Enqueue(text string)
}
