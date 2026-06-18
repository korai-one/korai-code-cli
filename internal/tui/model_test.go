package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// testCommands returns a registry with the built-in commands for TUI tests.
func testCommands() *command.Registry {
	r := command.NewRegistry()
	command.RegisterBuiltins(r, func() []string { return []string{"ReadFile"} })
	return r
}

// fakeRunner emits a fixed sequence of engine events on a closed channel.
type fakeRunner struct {
	events []engine.Event
}

func (f fakeRunner) Run(_ context.Context, _ []apiclient.Message, _ string) <-chan engine.Event {
	ch := make(chan engine.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch
}

// ready returns a model sized so the viewport is initialized.
func ready(r Runner) Model {
	m := New(r, NewAsker(), "system", testCommands())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return tm.(Model)
}

func lastEntry(m Model) entry {
	return m.entries[len(m.entries)-1]
}

func TestStreamingAppendsToOneAssistantEntry(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})

	dummy := make(chan engine.Event)
	tm, _ := m.Update(engineEventMsg{event: engine.TextEvent{Text: "Hello"}, ch: dummy})
	m = tm.(Model)
	tm, _ = m.Update(engineEventMsg{event: engine.TextEvent{Text: " world"}, ch: dummy})
	m = tm.(Model)

	if len(m.entries) != 1 {
		t.Fatalf("got %d entries, want 1 merged assistant entry", len(m.entries))
	}
	if e := lastEntry(m); e.kind != kindAssistant || e.text != "Hello world" {
		t.Errorf("entry = %+v, want assistant 'Hello world'", e)
	}
	if !m.streaming {
		t.Error("streaming should be true after text events")
	}
}

func TestToolEventsRecorded(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	dummy := make(chan engine.Event)

	tm, _ := m.Update(engineEventMsg{
		event: engine.ToolStartEvent{Name: "Bash", Input: []byte(`{"command":"ls"}`)},
		ch:    dummy,
	})
	m = tm.(Model)
	if m.streaming {
		t.Error("streaming should reset when a tool starts")
	}
	if e := lastEntry(m); e.kind != kindTool || !strings.Contains(e.text, "Bash") {
		t.Errorf("entry = %+v, want tool entry mentioning Bash", e)
	}

	tm, _ = m.Update(engineEventMsg{
		event: engine.ToolResultEvent{Name: "Bash", Result: tool.Result{Content: "boom", IsError: true}},
		ch:    dummy,
	})
	m = tm.(Model)
	if e := lastEntry(m); e.kind != kindToolResult || !strings.Contains(e.text, "boom") {
		t.Errorf("entry = %+v, want tool-result entry mentioning boom", e)
	}
}

func TestDoneEventCarriesHistory(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.busy = true
	dummy := make(chan engine.Event)

	hist := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hi"}}},
	}
	tm, _ := m.Update(engineEventMsg{event: engine.DoneEvent{Messages: hist}, ch: dummy})
	m = tm.(Model)

	if m.busy {
		t.Error("busy should be false after DoneEvent")
	}
	if len(m.history) != 1 {
		t.Errorf("history len = %d, want 1 (carried from DoneEvent)", len(m.history))
	}
}

func TestErrorEventShown(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.busy = true
	dummy := make(chan engine.Event)

	tm, _ := m.Update(engineEventMsg{event: engine.ErrorEvent{Err: context.Canceled}, ch: dummy})
	m = tm.(Model)

	if m.busy {
		t.Error("busy should be false after ErrorEvent")
	}
	if e := lastEntry(m); e.kind != kindError {
		t.Errorf("entry kind = %v, want error", e.kind)
	}
}

func TestSubmitStartsTurn(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{events: []engine.Event{engine.DoneEvent{}}})
	m.input.SetValue("do something")

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if !m.busy {
		t.Error("busy should be true after submit")
	}
	if cmd == nil {
		t.Error("submit should return a command to read events")
	}
	if m.input.Value() != "" {
		t.Error("input should be cleared after submit")
	}
	if len(m.entries) != 1 || m.entries[0].kind != kindUser {
		t.Fatalf("want one user entry, got %+v", m.entries)
	}
	if len(m.history) != 1 {
		t.Errorf("history should contain the submitted user message, got %d", len(m.history))
	}
}

func TestSlashClearResetsTranscript(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.addEntry(kindUser, "old")
	m.input.SetValue("/clear")

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if len(m.entries) != 0 {
		t.Errorf("transcript should be cleared, got %d entries", len(m.entries))
	}
	if m.busy {
		t.Error("a local command must not start a turn")
	}
}

func TestSlashHelpShowsText(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.input.SetValue("/help")

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if m.busy {
		t.Error("/help must not start a turn")
	}
	if e := lastEntry(m); e.kind != kindInfo || !strings.Contains(e.text, "/help") {
		t.Errorf("expected help text entry, got %+v", e)
	}
}

func TestSlashUnknownCommand(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.input.SetValue("/nope")

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if e := lastEntry(m); e.kind != kindError || !strings.Contains(e.text, "unknown command") {
		t.Errorf("expected unknown-command error, got %+v", e)
	}
}

// promptCmd is a fake skill-style command that expands to a prompt.
type promptCmd struct{}

func (promptCmd) Name() string        { return "explain" }
func (promptCmd) Description() string { return "expand to a prompt" }
func (promptCmd) Run(args string) (command.Result, error) {
	return command.Result{Action: command.SubmitPrompt, Text: "explain this: " + args}, nil
}

func TestSlashPromptCommandStartsTurn(t *testing.T) {
	t.Parallel()
	reg := command.NewRegistry()
	reg.Register(promptCmd{})

	m := New(fakeRunner{events: []engine.Event{engine.DoneEvent{}}}, NewAsker(), "system", reg)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = tm.(Model)
	m.input.SetValue("/explain the loop")

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if !m.busy {
		t.Error("a SubmitPrompt command should start a turn")
	}
	if cmd == nil {
		t.Error("expected a command to read events")
	}
	if len(m.history) != 1 {
		t.Fatalf("history len = %d, want 1", len(m.history))
	}
	got := m.history[0].Content[0].(apiclient.TextBlock).Text
	if got != "explain this: the loop" {
		t.Errorf("submitted prompt = %q, want expanded skill text", got)
	}
}

func TestSubmitEmptyIgnored(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.input.SetValue("   ")

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	if m.busy || len(m.entries) != 0 {
		t.Error("blank submit should be ignored")
	}
}

func TestPermissionDialogClearsAndLogs(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	pr := permRequest{req: perm.Request{ToolName: "Bash"}, reply: make(chan perm.Decision, 1)}

	tm, _ := m.Update(permRequestMsg{pr: pr})
	m = tm.(Model)
	if m.pending == nil {
		t.Fatal("pending request should be set")
	}

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = tm.(Model)
	if m.pending != nil {
		t.Error("pending should clear after a decision")
	}
	if cmd == nil {
		t.Fatal("expected a command delivering the decision")
	}
	if e := lastEntry(m); e.kind != kindInfo || !strings.Contains(e.text, "allowed") {
		t.Errorf("entry = %+v, want info noting it was allowed", e)
	}
}

func TestReplyPermissionDelivers(t *testing.T) {
	t.Parallel()
	reply := make(chan perm.Decision, 1)
	pr := permRequest{reply: reply}

	if msg := replyPermission(pr, perm.DecisionAllow)(); msg != nil {
		t.Errorf("replyPermission msg = %v, want nil", msg)
	}
	if d := <-reply; d != perm.DecisionAllow {
		t.Errorf("delivered decision = %v, want allow", d)
	}
}

func TestDialogDenyOnEscape(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	pr := permRequest{req: perm.Request{ToolName: "Bash"}, reply: make(chan perm.Decision, 1)}

	tm, _ := m.Update(permRequestMsg{pr: pr})
	m = tm.(Model)
	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(Model)

	if m.pending != nil {
		t.Error("pending should clear on escape")
	}
	if cmd == nil {
		t.Fatal("expected a command delivering the denial")
	}
	if e := lastEntry(m); e.kind != kindInfo || !strings.Contains(e.text, "denied") {
		t.Errorf("entry = %+v, want info noting it was denied", e)
	}
}

func TestAskerRoundTrip(t *testing.T) {
	t.Parallel()
	a := NewAsker()

	type result struct {
		d   perm.Decision
		err error
	}
	done := make(chan result, 1)
	go func() {
		d, err := a.Ask(context.Background(), perm.Request{ToolName: "Write"})
		done <- result{d, err}
	}()

	pr := <-a.requests
	if pr.req.ToolName != "Write" {
		t.Errorf("tool = %q, want Write", pr.req.ToolName)
	}
	pr.reply <- perm.DecisionAllow

	got := <-done
	if got.err != nil {
		t.Fatalf("Ask: %v", got.err)
	}
	if got.d != perm.DecisionAllow {
		t.Errorf("decision = %v, want allow", got.d)
	}
}

func TestAskerCancelled(t *testing.T) {
	t.Parallel()
	a := NewAsker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d, err := a.Ask(ctx, perm.Request{ToolName: "Write"})
	if err == nil {
		t.Error("expected error when context is cancelled")
	}
	if d != perm.DecisionDeny {
		t.Errorf("decision = %v, want deny on cancellation (fail-closed)", d)
	}
}

func TestSlashCompactRunsCompactor(t *testing.T) {
	t.Parallel()
	reg := command.NewRegistry()
	reg.Register(command.NewCompactCommand())

	called := false
	compactor := func(_ context.Context, h []apiclient.Message) ([]apiclient.Message, error) {
		called = true
		return h[len(h)-1:], nil // keep only the last message
	}

	m := New(fakeRunner{}, NewAsker(), "system", reg).WithCompactor(compactor)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = tm.(Model)
	m.history = []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "a"}}},
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "b"}}},
	}
	m.input.SetValue("/compact")

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if !m.busy {
		t.Error("compaction should mark the model busy")
	}
	if cmd == nil {
		t.Fatal("expected a command to run the compactor")
	}

	// Run the batch and feed the compactDoneMsg back (ignoring the spinner tick).
	var done tea.Msg
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if mm := c(); mm != nil {
				if _, isCompact := mm.(compactDoneMsg); isCompact {
					done = mm
				}
			}
		}
	}
	if done == nil {
		t.Fatal("no compactDoneMsg produced")
	}
	tm, _ = m.Update(done)
	m = tm.(Model)

	if !called {
		t.Error("compactor was not invoked")
	}
	if len(m.history) != 1 {
		t.Errorf("history len = %d, want 1 after compaction", len(m.history))
	}
	if m.busy {
		t.Error("busy should clear after compaction completes")
	}
}

func TestCompactUnavailable(t *testing.T) {
	t.Parallel()
	reg := command.NewRegistry()
	reg.Register(command.NewCompactCommand())

	m := New(fakeRunner{}, NewAsker(), "system", reg) // no compactor
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = tm.(Model)
	m.history = []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "a"}}},
	}
	m.input.SetValue("/compact")

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.busy {
		t.Error("compaction must not start without a compactor")
	}
	if e := lastEntry(m); e.kind != kindInfo || !strings.Contains(e.text, "unavailable") {
		t.Errorf("expected unavailable info, got %+v", e)
	}
}
