package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestBackslashContinuation accumulates a multi-line prompt across submits.
func TestBackslashContinuation(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})

	m.input.SetValue(`line1\`)
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.draft != "line1\n" {
		t.Fatalf("draft = %q, want %q", m.draft, "line1\n")
	}
	if m.busy {
		t.Error("a continued line should not start a turn")
	}

	m.input.SetValue("line2")
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if m.draft != "" {
		t.Errorf("draft should clear after submit, got %q", m.draft)
	}
	var sawPrompt bool
	for _, e := range m.entries {
		if e.kind == kindUser && e.text == "line1\nline2" {
			sawPrompt = true
		}
	}
	if !sawPrompt {
		t.Errorf("entries missing the joined multi-line prompt: %+v", m.entries)
	}
}

// TestInputHistoryNavigation recalls prior prompts with up/down.
func TestInputHistoryNavigation(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.inputHist.add("first")
	m.inputHist.add("second")

	up := func() { tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp}); m = tm.(Model) }
	down := func() { tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}); m = tm.(Model) }

	up()
	if m.input.Value() != "second" {
		t.Errorf("after one up, input = %q, want second", m.input.Value())
	}
	up()
	if m.input.Value() != "first" {
		t.Errorf("after two ups, input = %q, want first", m.input.Value())
	}
	down()
	if m.input.Value() != "second" {
		t.Errorf("after down, input = %q, want second", m.input.Value())
	}
}

// TestSessionAllowAutoApproves skips the dialog for a session-allowed tool.
func TestSessionAllowAutoApproves(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.sessionAllowed["Bash"] = true

	pr := permRequest{req: perm.Request{ToolName: "Bash"}, reply: make(chan perm.Decision, 1)}
	tm, cmd := m.Update(permRequestMsg{pr: pr})
	m = tm.(Model)
	if m.pending != nil {
		t.Error("session-allowed tool should not raise a dialog")
	}
	if cmd == nil {
		t.Error("expected a reply command")
	}
}

// TestPermissionAllowForSession records the tool when the user picks "always".
func TestPermissionAllowForSession(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	pr := permRequest{req: perm.Request{ToolName: "Bash"}, reply: make(chan perm.Decision, 1)}
	m.pending = &pr

	tm, _ := m.Update(keyRunes("a"))
	m = tm.(Model)
	if m.pending != nil {
		t.Error("dialog should close after a decision")
	}
	if !m.sessionAllowed["Bash"] {
		t.Error(`"always" should record the tool as session-allowed`)
	}
}

// TestEditResultRendersDiff shows a diff block under a successful Edit.
func TestEditResultRendersDiff(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	dummy := make(chan engine.Event)

	input := []byte(`{"path":"f.go","old_string":"foo","new_string":"bar"}`)
	tm, _ := m.Update(engineEventMsg{event: engine.ToolStartEvent{Name: "Edit", Input: input}, ch: dummy})
	m = tm.(Model)
	if m.pendingEdit == nil {
		t.Fatal("Edit start should capture the pending change")
	}

	tm, _ = m.Update(engineEventMsg{
		event: engine.ToolResultEvent{Name: "Edit", Result: tool.Result{Content: "ok"}},
		ch:    dummy,
	})
	m = tm.(Model)
	if e := lastEntry(m); e.kind != kindDiff {
		t.Errorf("last entry kind = %v, want kindDiff", e.kind)
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit should clear after the result")
	}
}

// TestEditErrorSkipsDiff suppresses the diff when the Edit failed.
func TestEditErrorSkipsDiff(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	dummy := make(chan engine.Event)

	input := []byte(`{"path":"f.go","old_string":"foo","new_string":"bar"}`)
	tm, _ := m.Update(engineEventMsg{event: engine.ToolStartEvent{Name: "Edit", Input: input}, ch: dummy})
	m = tm.(Model)
	tm, _ = m.Update(engineEventMsg{
		event: engine.ToolResultEvent{Name: "Edit", Result: tool.Result{Content: "boom", IsError: true}},
		ch:    dummy,
	})
	m = tm.(Model)
	if e := lastEntry(m); e.kind == kindDiff {
		t.Error("a failed Edit should not render a diff")
	}
}

// TestSearchModeEnterExit toggles find mode with ctrl+f and esc.
func TestSearchModeEnterExit(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = tm.(Model)
	if !m.searching {
		t.Fatal("ctrl+f should enter search mode")
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(Model)
	if m.searching {
		t.Error("esc should exit search mode")
	}
}

// TestArgHintLoneSlashNoPanic guards the regression where typing just "/"
// (the first keystroke of any slash command) panicked in View.
func TestArgHintLoneSlashNoPanic(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.input.SetValue("/")
	if got := m.argHint(); got != "" {
		t.Errorf("argHint(%q) = %q, want empty", "/", got)
	}
	// View must not panic and must render something.
	if v := m.View(); v == "" {
		t.Error("View returned empty with a lone slash")
	}
}

// TestArgHintKnownCommand shows the description of a typed command.
func TestArgHintKnownCommand(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.input.SetValue("/help")
	if got := m.argHint(); !strings.Contains(got, "commands") {
		t.Errorf("argHint(/help) = %q, want it to mention the help description", got)
	}
}

// TestViewportLeavesRoomForChrome checks the transcript viewport is sized so the
// status line, mode badge, and prompt all fit on screen.
func TestViewportLeavesRoomForChrome(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{}).
		WithModels(apiclient.NewModelSelector("claude-sonnet-4-6")).
		WithModes(perm.NewModeSelector(perm.ModePlan))
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = tm.(Model)

	if got, want := m.viewport.Height, 24-m.chromeLines(); got != want {
		t.Errorf("viewport height = %d, want %d (24 - %d chrome)", got, want, m.chromeLines())
	}
	if m.viewport.Height >= 24 {
		t.Errorf("viewport height %d leaves no room for chrome", m.viewport.Height)
	}
}

// TestSearchMatchesEntries runs an incremental search over the transcript.
func TestSearchMatchesEntries(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.addEntry(kindAssistant, "alpha")
	m.addEntry(kindAssistant, "bravo")

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = tm.(Model)
	tm, _ = m.Update(keyRunes("a"))
	m = tm.(Model)

	if got := len(m.search.hits()); got != 2 {
		t.Errorf(`query "a" matched %d entries, want 2`, got)
	}
}
