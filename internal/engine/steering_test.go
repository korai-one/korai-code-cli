package engine

import (
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// TestEnqueueDrainMergesIntoTrailingUser checks queued steer text is appended to
// a trailing user message (keeping roles valid) and that the queue then empties.
func TestEnqueueDrainMergesIntoTrailingUser(t *testing.T) {
	e := &Engine{}
	e.Enqueue("   ") // blank is ignored
	e.Enqueue("focus on the tests")

	h := []apiclient.Message{{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "fix the bug"}},
	}}
	got := e.drainSteering(h)
	if len(got) != 1 {
		t.Fatalf("want 1 message, got %d", len(got))
	}
	if len(got[0].Content) != 2 {
		t.Fatalf("steer should append to the trailing user message: %+v", got[0].Content)
	}
	tb, ok := got[0].Content[1].(apiclient.TextBlock)
	if !ok || tb.Text != "focus on the tests" {
		t.Errorf("appended block = %+v", got[0].Content[1])
	}

	// The queue is now drained: a second call must add nothing.
	got2 := e.drainSteering(got)
	if len(got2[0].Content) != 2 {
		t.Errorf("second drain should be a no-op, got %+v", got2[0].Content)
	}
}

// TestDrainSteeringNewUserAfterAssistant adds a fresh user turn when the trailing
// message is an assistant turn (can't append to an assistant message).
func TestDrainSteeringNewUserAfterAssistant(t *testing.T) {
	e := &Engine{}
	e.Enqueue("stop and explain")

	h := []apiclient.Message{{
		Role:    apiclient.RoleAssistant,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "thinking"}},
	}}
	got := e.drainSteering(h)
	if len(got) != 2 || got[1].Role != apiclient.RoleUser {
		t.Fatalf("steer after assistant should add a user message: %+v", got)
	}
}

// TestDrainSteeringEmpty is a no-op when nothing is queued.
func TestDrainSteeringEmpty(t *testing.T) {
	e := &Engine{}
	h := []apiclient.Message{{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "x"}},
	}}
	if got := e.drainSteering(h); len(got[0].Content) != 1 {
		t.Errorf("no steer queued should be a no-op: %+v", got)
	}
}
