package tui

import (
	"time"

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

// planRequestMsg surfaces a pending plan-approval request to the model.
type planRequestMsg struct {
	pr planRequest
}

// resumeLoadedMsg carries the result of loading a saved session.
type resumeLoadedMsg struct {
	id       string
	created  time.Time
	messages []apiclient.Message
	err      error
}

// waitForPlan blocks until the plan approver hands over a request.
func waitForPlan(a *PlanApprover) tea.Cmd {
	return func() tea.Msg {
		return planRequestMsg{pr: <-a.requests}
	}
}

// replyPlan delivers an approval decision back to a blocked ApprovePlan call.
func replyPlan(pr planRequest, approved bool) tea.Cmd {
	return func() tea.Msg {
		pr.reply <- approved
		return nil
	}
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
