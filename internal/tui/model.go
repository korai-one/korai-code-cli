// Package tui is the Bubble Tea interactive REPL. It consumes engine events and
// renders the transcript, streaming output, and permission dialogs.
//
// Elm discipline (AGENTS.md §4.3): Update is pure and fast, every blocking
// operation lives in a tea.Cmd, and View only renders. The engine's event
// channel and the interactive Asker are both bridged into messages via Cmds.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
)

type entryKind int

const (
	kindUser entryKind = iota
	kindAssistant
	kindTool
	kindToolResult
	kindError
	kindInfo
)

// entry is one rendered line group in the transcript.
type entry struct {
	kind entryKind
	text string
}

// Model is the Bubble Tea model for the REPL.
type Model struct {
	runner   Runner
	asker    *Asker
	system   string
	commands *command.Registry

	history   []apiclient.Message
	entries   []entry
	streaming bool // an assistant entry is currently being appended to

	input    textinput.Model
	spinner  spinner.Model
	viewport viewport.Model
	styles   styles

	compactor Compactor
	modes     *perm.ModeSelector

	busy    bool
	pending *permRequest
	cancel  context.CancelFunc

	width, height int
	ready         bool
	quitting      bool
}

// New builds a REPL model bound to a Runner, the interactive Asker, the system
// prompt, and the slash-command registry. A nil registry disables slash commands.
func New(runner Runner, asker *Asker, system string, commands *command.Registry) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask Korai…"
	ti.Prompt = "› "
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		runner:   runner,
		asker:    asker,
		system:   system,
		commands: commands,
		input:    ti,
		spinner:  sp,
		styles:   newStyles(),
	}
}

// Compactor summarizes the conversation history, returning a shorter history.
type Compactor func(ctx context.Context, history []apiclient.Message) ([]apiclient.Message, error)

// WithCompactor returns a copy of the model wired to run /compact via c. Call
// before handing the model to tea.NewProgram.
func (m Model) WithCompactor(c Compactor) Model {
	m.compactor = c
	return m
}

// WithModes returns a copy of the model wired to the shared permission-mode
// selector, enabling shift+tab cycling and the mode indicator. Call before
// handing the model to tea.NewProgram.
func (m Model) WithModes(s *perm.ModeSelector) Model {
	m.modes = s
	return m
}

// Init starts the input cursor blink and begins listening for permission
// requests from the engine.
func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForPermission(m.asker))
}

// Update is the pure state transition. All I/O is deferred to the returned Cmd.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.onResize(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case permRequestMsg:
		m.pending = &msg.pr
		return m, nil
	case engineEventMsg:
		return m.onEngineEvent(msg)
	case compactDoneMsg:
		m.busy = false
		if msg.err != nil {
			m.addEntry(kindError, "compaction failed: "+msg.err.Error())
		} else {
			m.history = msg.history
			m.addEntry(kindInfo, fmt.Sprintf("compacted; %d messages retained", len(msg.history)))
		}
		return m, nil
	case turnDoneMsg:
		m.busy = false
		m.streaming = false
		return m, nil
	}
	return m, nil
}

func (m Model) onResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.input.Width = msg.Width - 4

	// Reserve two lines below the transcript: a status line and the input.
	vpHeight := msg.Height - 2
	if vpHeight < 1 {
		vpHeight = 1
	}
	if !m.ready {
		m.viewport = viewport.New(msg.Width, vpHeight)
		m.ready = true
	} else {
		m.viewport.Width = msg.Width
		m.viewport.Height = vpHeight
	}
	m.refreshViewport()
	return m, nil
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		m.quitting = true
		return m, tea.Quit
	}

	// Permission dialog takes priority over all other input.
	if m.pending != nil {
		return m.onDialogKey(msg)
	}

	// Shift+Tab cycles the permission mode (default → acceptEdits → plan).
	if msg.Type == tea.KeyShiftTab && m.modes != nil {
		m.addEntry(kindInfo, "permission mode: "+m.modes.Cycle().String())
		return m, nil
	}

	if m.busy {
		if msg.String() == "esc" && m.cancel != nil {
			m.cancel()
			m.addEntry(kindInfo, "interrupted")
		}
		return m, nil
	}

	if msg.Type == tea.KeyEnter {
		return m.submit()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) onDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var decision perm.Decision
	switch msg.String() {
	case "y", "Y":
		decision = perm.DecisionAllow
	case "n", "N", "esc":
		decision = perm.DecisionDeny
	default:
		return m, nil
	}

	pr := *m.pending
	m.pending = nil
	verb := "denied"
	if decision == perm.DecisionAllow {
		verb = "allowed"
	}
	m.addEntry(kindInfo, fmt.Sprintf("%s %s", verb, pr.req.ToolName))
	return m, tea.Batch(replyPermission(pr, decision), waitForPermission(m.asker))
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	// Slash commands are handled locally and never reach the model directly.
	if name, args, ok := command.Parse(text); ok && m.commands != nil {
		return m.dispatchCommand(name, args, text)
	}

	return m.startTurn(text)
}

// dispatchCommand runs a slash command and acts on its Result.
func (m Model) dispatchCommand(name, args, raw string) (tea.Model, tea.Cmd) {
	cmd, ok := m.commands.Get(name)
	if !ok {
		m.addEntry(kindError, "unknown command: /"+name)
		return m, nil
	}
	res, err := cmd.Run(args)
	if err != nil {
		m.addEntry(kindError, err.Error())
		return m, nil
	}
	switch res.Action {
	case command.ShowText:
		m.addEntry(kindInfo, res.Text)
		return m, nil
	case command.Clear:
		m.entries = nil
		m.history = nil
		m.refreshViewport()
		return m, nil
	case command.Quit:
		m.quitting = true
		return m, tea.Quit
	case command.SubmitPrompt:
		m.addEntry(kindUser, raw)
		return m.startTurn(res.Text)
	case command.CompactHistory:
		return m.startCompaction()
	default:
		return m, nil
	}
}

// startCompaction runs the injected compactor over the current history.
func (m Model) startCompaction() (tea.Model, tea.Cmd) {
	if m.compactor == nil {
		m.addEntry(kindInfo, "compaction is unavailable")
		return m, nil
	}
	if len(m.history) == 0 {
		m.addEntry(kindInfo, "nothing to compact")
		return m, nil
	}
	m.addEntry(kindInfo, "compacting conversation…")
	m.busy = true
	history := m.history
	compactor := m.compactor
	return m, tea.Batch(
		func() tea.Msg {
			compacted, err := compactor(context.Background(), history)
			return compactDoneMsg{history: compacted, err: err}
		},
		m.spinner.Tick,
	)
}

// startTurn records the prompt and launches an engine turn.
func (m Model) startTurn(promptText string) (tea.Model, tea.Cmd) {
	if !strings.HasPrefix(strings.TrimSpace(promptText), "/") {
		m.addEntry(kindUser, promptText)
	}
	m.history = append(m.history, apiclient.Message{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: promptText}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch := m.runner.Run(ctx, m.history, m.system)
	m.busy = true
	m.streaming = false

	return m, tea.Batch(waitForEvent(ch), m.spinner.Tick)
}

func (m Model) onEngineEvent(msg engineEventMsg) (tea.Model, tea.Cmd) {
	switch ev := msg.event.(type) {
	case engine.TextEvent:
		if m.streaming && len(m.entries) > 0 {
			m.entries[len(m.entries)-1].text += ev.Text
		} else {
			m.entries = append(m.entries, entry{kind: kindAssistant, text: ev.Text})
			m.streaming = true
		}
	case engine.ToolStartEvent:
		m.streaming = false
		m.addEntry(kindTool, fmt.Sprintf("%s %s", ev.Name, strings.TrimSpace(string(ev.Input))))
	case engine.ToolResultEvent:
		m.streaming = false
		if ev.Result.IsError {
			m.addEntry(kindToolResult, "error: "+oneLine(ev.Result.Content))
		} else {
			m.addEntry(kindToolResult, oneLine(ev.Result.Content))
		}
	case engine.DoneEvent:
		m.history = ev.Messages
		m.busy = false
		m.streaming = false
		m.refreshViewport()
		return m, nil
	case engine.ErrorEvent:
		m.addEntry(kindError, ev.Err.Error())
		m.busy = false
		m.streaming = false
		m.refreshViewport()
		return m, nil
	}
	m.refreshViewport()
	return m, waitForEvent(msg.ch)
}

// addEntry appends a transcript entry and scrolls to the bottom.
func (m *Model) addEntry(kind entryKind, text string) {
	m.entries = append(m.entries, entry{kind: kind, text: text})
	m.refreshViewport()
}

func (m *Model) refreshViewport() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.renderEntries())
	m.viewport.GotoBottom()
}

func (m Model) renderEntries() string {
	w := m.viewport.Width
	var b strings.Builder
	for i, e := range m.entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch e.kind {
		case kindUser:
			b.WriteString(m.styles.user.Width(w).Render("› " + e.text))
		case kindAssistant:
			b.WriteString(m.styles.assistant.Width(w).Render(e.text))
		case kindTool:
			b.WriteString(m.styles.tool.Width(w).Render("⚙ " + e.text))
		case kindToolResult:
			b.WriteString(m.styles.toolResult.Width(w).Render("  ↳ " + e.text))
		case kindError:
			b.WriteString(m.styles.errorText.Width(w).Render("✗ " + e.text))
		case kindInfo:
			b.WriteString(m.styles.info.Width(w).Render("• " + e.text))
		}
	}
	return b.String()
}

// View renders the current frame.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "initializing…"
	}

	var bottom string
	switch {
	case m.pending != nil:
		bottom = m.renderDialog()
	case m.busy:
		bottom = m.spinner.View() + " working… (esc to interrupt)"
	default:
		bottom = m.input.View()
	}

	lines := []string{m.viewport.View()}
	if badge := m.modeBadge(); badge != "" {
		lines = append(lines, badge)
	}
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

// modeBadge renders the current permission-mode indicator shown above the input.
// The default mode ("no mode") shows nothing.
func (m Model) modeBadge() string {
	if m.modes == nil {
		return ""
	}
	switch m.modes.Get() {
	case perm.ModePlan:
		return m.styles.info.Render("⏸ plan mode — read-only · shift+tab to cycle")
	case perm.ModeAcceptEdits:
		return m.styles.info.Render("✎ accept edits — files auto-approved · shift+tab to cycle")
	case perm.ModeBypassPermissions:
		return m.styles.errorText.Render("⚠ bypass permissions — all tools auto-approved")
	default:
		return ""
	}
}

func (m Model) renderDialog() string {
	pr := m.pending
	body := fmt.Sprintf("Allow %s?  [y]es / [n]o", pr.req.ToolName)
	if args := oneLine(string(pr.req.Input)); args != "" {
		body = fmt.Sprintf("Allow %s?\n%s\n[y]es / [n]o", pr.req.ToolName, args)
	}
	return m.styles.dialog.Render(body)
}

// oneLine collapses content to a single trimmed line, truncated for display.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	const maxLen = 120
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}
