package tui

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Nevaero/korai-code-cli/internal/engine"
)

// TestREPLEndToEnd drives the full Bubble Tea loop with a scripted runner:
// type a prompt, submit, and confirm the streamed assistant text reaches the
// screen. This exercises the real Update/Cmd/View cycle, not just handlers.
func TestREPLEndToEnd(t *testing.T) {
	t.Parallel()

	runner := fakeRunner{events: []engine.Event{
		engine.TextEvent{Text: "Hello from Korai"},
		engine.DoneEvent{},
	}}
	m := New(runner, NewAsker(), "system", nil)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	tm.Type("hi there")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("Hello from Korai"))
	}, teatest.WithDuration(3*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(Model)

	var sawUser, sawAssistant bool
	for _, e := range fm.entries {
		switch {
		case e.kind == kindUser && e.text == "hi there":
			sawUser = true
		case e.kind == kindAssistant && e.text == "Hello from Korai":
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Error("final model missing the user entry")
	}
	if !sawAssistant {
		t.Error("final model missing the streamed assistant entry")
	}
}
