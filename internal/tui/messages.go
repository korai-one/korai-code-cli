package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
)

// engineEventMsg carries one engine event plus the channel it came from, so the
// model can re-subscribe for the next event.
type engineEventMsg struct {
	event engine.Event
	ch    <-chan engine.Event
}

// turnDoneMsg signals the engine's event channel closed (turn finished).
type turnDoneMsg struct{}

// permRequestMsg surfaces a pending permission request to the model.
type permRequestMsg struct {
	pr permRequest
}

// compactDoneMsg carries the result of a /compact run.
type compactDoneMsg struct {
	history []apiclient.Message
	err     error
}

// waitForEvent reads the next engine event. When the channel is closed it
// reports the turn is done.
func waitForEvent(ch <-chan engine.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return turnDoneMsg{}
		}
		return engineEventMsg{event: ev, ch: ch}
	}
}

// waitForPermission blocks until the asker hands over a permission request.
func waitForPermission(a *Asker) tea.Cmd {
	return func() tea.Msg {
		return permRequestMsg{pr: <-a.requests}
	}
}

// replyPermission delivers a decision back to a blocked Ask call.
func replyPermission(pr permRequest, d perm.Decision) tea.Cmd {
	return func() tea.Msg {
		pr.reply <- d
		return nil
	}
}
