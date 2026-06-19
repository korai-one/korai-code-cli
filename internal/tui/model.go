// Package tui is the Bubble Tea interactive REPL. It consumes engine events and
// renders the transcript, streaming output, and permission dialogs.
//
// Elm discipline (AGENTS.md §4.3): Update is pure and fast, every blocking
// operation lives in a tea.Cmd, and View only renders. The engine's event
// channel and the interactive Asker are both bridged into messages via Cmds.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/command"
	"github.com/Nevaero/korai-code-cli/internal/cost"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	plantool "github.com/Nevaero/korai-code-cli/internal/tools/plan"
)

type entryKind int

const (
	kindUser entryKind = iota
	kindAssistant
	kindTool
	kindToolResult
	kindDiff // pre-styled diff block shown under an Edit's result
	kindError
	kindInfo
)

// entry is one rendered line group in the transcript.
type entry struct {
	kind entryKind
	text string

	// rendered caches the markdown-rendered form of an assistant entry at
	// renderedWidth, so glamour runs once per entry per width rather than on
	// every streamed token. Empty until the entry is finalized.
	rendered      string
	renderedWidth int

	// diffOld/diffNew hold the before/after text of a kindDiff entry, rendered
	// to a +/- block at the current width (so it reflows on resize).
	diffOld, diffNew string
}

// Model is the Bubble Tea model for the REPL.
type Model struct {
	runner   Runner
	asker    *Asker
	system   string
	commands *command.Registry
	version  string

	history   []apiclient.Message
	entries   []entry
	streaming bool // an assistant entry is currently being appended to

	input    textinput.Model
	spinner  spinner.Model
	viewport viewport.Model
	styles   styles
	md       *markdownRenderer

	inputHist inputHistory // ↑/↓ recall of submitted prompts
	draft     string       // accumulated lines from "\"-continued input

	menu        []command.Command // live slash-command suggestions ("/" menu)
	menuIdx     int               // selected suggestion
	menuHideFor string            // input value the menu was dismissed for (esc)

	atItems      []string // live @-mention file suggestions
	atIdx        int      // selected file suggestion
	atHideFor    string   // input value the @-menu was dismissed for (esc)
	files        []string // workspace file candidates, loaded lazily
	filesLoaded  bool
	filesLoading bool
	// fileFinder lists workspace-relative paths for @-mentions; mentionExpander
	// inlines the content of @-referenced files into the submitted prompt. Both
	// are injected so the model does no filesystem I/O itself.
	fileFinder      func() []string
	mentionExpander func(string) string

	search    transcriptSearch // transcript find state (ctrl+f)
	searching bool             // the input is acting as a search box

	compactor    Compactor
	modes        *perm.ModeSelector
	models       *apiclient.ModelSelector
	cost         *cost.Tracker
	planApprover *PlanApprover

	// sessionAllowed records tool names the user chose to allow for the rest of
	// the session ("[a]lways" in the permission dialog), so repeat calls skip the
	// prompt. It never persists and never widens the engine's own rules.
	sessionAllowed map[string]bool
	// pendingEdit holds the old/new text of an in-flight Edit, captured at
	// ToolStart and rendered as a diff when its result arrives.
	pendingEdit *editChange

	saver        Saver
	resumeLoader ResumeLoader
	sessionID    string
	sessionStart time.Time

	busy         bool
	pending      *permRequest
	pendingPlan  *planRequest
	planChoice   int  // selected option in the plan-approval dialog
	planFeedback bool // collecting "keep planning" feedback in the input
	cancel       context.CancelFunc

	width, height int
	ready         bool
	quitting      bool

	// entryOffsets[i] is the line at which entry i begins in the rendered
	// transcript, recomputed on each refresh and used to scroll to search hits.
	entryOffsets []int
}

// editChange is the before/after text of an Edit tool call, used to render a diff.
type editChange struct {
	old, new string
}

// New builds a REPL model bound to a Runner, the interactive Asker, the system
// prompt, and the slash-command registry. A nil registry disables slash commands.
func New(runner Runner, asker *Asker, system string, commands *command.Registry) Model {
	ti := textinput.New()
	ti.Placeholder = "Ask Korai…"
	ti.Prompt = "› "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(colPurple)
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colPurple)

	return Model{
		runner:         runner,
		asker:          asker,
		system:         system,
		commands:       commands,
		input:          ti,
		spinner:        sp,
		styles:         newStyles(),
		sessionAllowed: make(map[string]bool),
	}
}

// WithVersion sets the version string shown in the start-up welcome banner.
// Call before tea.NewProgram.
func (m Model) WithVersion(v string) Model {
	m.version = v
	return m
}

// WithFileFinder wires the workspace file lister used by @-mention completion.
// finder returns workspace-relative, slash-separated paths. Call before
// tea.NewProgram.
func (m Model) WithFileFinder(finder func() []string) Model {
	m.fileFinder = finder
	return m
}

// WithMentionExpander wires the function that inlines @-referenced file contents
// into a submitted prompt. Call before tea.NewProgram.
func (m Model) WithMentionExpander(expand func(string) string) Model {
	m.mentionExpander = expand
	return m
}

// WithModels wires the active-model selector so the status line can show the
// current model. Call before tea.NewProgram.
func (m Model) WithModels(s *apiclient.ModelSelector) Model {
	m.models = s
	return m
}

// WithCost wires the token/cost tracker so the status line can show usage. Call
// before tea.NewProgram.
func (m Model) WithCost(t *cost.Tracker) Model {
	m.cost = t
	return m
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

// WithPlanApprover returns a copy of the model wired to handle ExitPlanMode
// approval requests from the given approver. Call before tea.NewProgram.
func (m Model) WithPlanApprover(a *PlanApprover) Model {
	m.planApprover = a
	return m
}

// Saver persists the conversation under a session id and creation time. Called
// after every completed turn so a session can be resumed later.
type Saver func(id string, created time.Time, messages []apiclient.Message)

// ResumeLoader loads a saved session by id, returning its messages and creation
// time. Used by /resume <id>.
type ResumeLoader func(id string) (messages []apiclient.Message, created time.Time, err error)

// WithSaver wires conversation auto-saving. Call before tea.NewProgram.
func (m Model) WithSaver(s Saver) Model {
	m.saver = s
	return m
}

// WithResumeLoader wires live /resume <id> loading. Call before tea.NewProgram.
func (m Model) WithResumeLoader(l ResumeLoader) Model {
	m.resumeLoader = l
	return m
}

// WithSession seeds the active session id, its creation time, and any prior
// history (e.g. from --resume or --continue). Call before tea.NewProgram.
func (m Model) WithSession(id string, created time.Time, history []apiclient.Message) Model {
	m.sessionID = id
	m.sessionStart = created
	m.history = history
	m.entries = entriesFromMessages(history)
	return m
}

// Init starts the input cursor blink and begins listening for permission and
// plan-approval requests from the engine.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, waitForPermission(m.asker)}
	if m.planApprover != nil {
		cmds = append(cmds, waitForPlan(m.planApprover))
	}
	return tea.Batch(cmds...)
}

// Update is the pure state transition. All I/O is deferred to the returned Cmd.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.onResize(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		// Mouse-wheel scrolling of the transcript; the viewport ignores other
		// mouse actions.
		if m.ready {
			m.viewport, _ = m.viewport.Update(msg)
		}
		return m, nil
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case permRequestMsg:
		// Tools the user chose to allow for the session skip the prompt.
		if m.sessionAllowed[msg.pr.req.ToolName] {
			return m, tea.Batch(replyPermission(msg.pr, perm.DecisionAllow), waitForPermission(m.asker))
		}
		m.pending = &msg.pr
		return m, nil
	case planRequestMsg:
		m.pendingPlan = &msg.pr
		m.planChoice = 0
		return m, nil
	case filesLoadedMsg:
		m.files = msg.paths
		m.filesLoaded = true
		m.filesLoading = false
		m.updateAt() // populate suggestions now that the list is in
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
	case resumeLoadedMsg:
		if msg.err != nil {
			m.addEntry(kindError, "resume failed: "+msg.err.Error())
			return m, nil
		}
		m.sessionID = msg.id
		m.sessionStart = msg.created
		m.history = msg.messages
		m.entries = entriesFromMessages(msg.messages)
		m.addEntry(kindInfo, fmt.Sprintf("resumed session %s (%d messages)", msg.id, len(msg.messages)))
		m.refreshViewport()
		return m, nil
	}
	return m, nil
}

// entriesFromMessages rebuilds the visible transcript from a saved conversation,
// showing the user and assistant text (tool internals are omitted).
func entriesFromMessages(msgs []apiclient.Message) []entry {
	var es []entry
	for _, msg := range msgs {
		for _, b := range msg.Content {
			tb, ok := b.(apiclient.TextBlock)
			if !ok || strings.TrimSpace(tb.Text) == "" {
				continue
			}
			kind := kindAssistant
			if msg.Role == apiclient.RoleUser {
				kind = kindUser
			}
			es = append(es, entry{kind: kind, text: tb.Text})
		}
	}
	return es
}

func (m Model) onResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.input.Width = msg.Width - 4

	if !m.ready {
		m.viewport = viewport.New(msg.Width, 1)
		m.ready = true
	} else {
		m.viewport.Width = msg.Width
	}
	// Rebuild the markdown renderer to wrap at the new width. Cached per-entry
	// renders at the old width are invalidated lazily via renderedWidth.
	if m.md == nil || m.md.width != msg.Width {
		m.md = newMarkdownRenderer(msg.Width)
	}
	m.relayout()
	m.refreshViewport()
	return m, nil
}

// chromeLines counts the lines rendered below the transcript (status line,
// mode badge, the prompt/dialog, and any "\"-continued draft), so the viewport
// can be sized to fill the rest of the screen without pushing the input off it.
func (m Model) chromeLines() int {
	n := 1 // the prompt / spinner / dialog line
	if m.statusLine() != "" {
		n++
	}
	if m.modeBadge() != "" {
		n++
	}
	if m.draft != "" {
		n += strings.Count(strings.TrimRight(m.draft, "\n"), "\n") + 1
	}
	if items, _ := m.menuWindow(); len(items) > 0 {
		n += len(items)
	}
	if items, _ := m.atWindow(); len(items) > 0 {
		n += len(items)
	}
	return n
}

// relayout resizes the transcript viewport to the screen height minus the chrome
// below it. Call after anything that changes the chrome (resize, mode cycle,
// draft growth).
func (m *Model) relayout() {
	if !m.ready {
		return
	}
	h := m.height - m.chromeLines()
	if h < 1 {
		h = 1
	}
	m.viewport.Height = h
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.cancel != nil {
			m.cancel()
		}
		m.quitting = true
		return m, tea.Quit
	}

	// Transcript scrolling works in any state (including while the agent is
	// busy or a dialog is open) so the user can read back through output. These
	// bindings avoid the single-line editing keys (home/end, ctrl+u, word moves).
	if m.ready && m.handleScroll(msg) {
		return m, nil
	}

	// Permission and plan-approval dialogs take priority over all other input,
	// matching View's render priority.
	if m.pending != nil {
		return m.onDialogKey(msg)
	}
	if m.pendingPlan != nil {
		if m.planFeedback {
			return m.onPlanFeedbackKey(msg)
		}
		return m.onPlanKey(msg)
	}

	// Search mode turns the input into a find box until esc.
	if m.searching {
		return m.onSearchKey(msg)
	}

	// Shift+Tab cycles the permission mode (default → acceptEdits → plan). The
	// current mode is shown by the badge above the input, so cycling does not
	// post a message into the transcript.
	if msg.Type == tea.KeyShiftTab && m.modes != nil {
		m.modes.Cycle()
		m.relayout() // the badge may appear or disappear
		m.refreshViewport()
		return m, nil
	}

	if m.busy {
		if msg.String() == "esc" && m.cancel != nil {
			m.cancel()
			m.addEntry(kindInfo, "interrupted")
		}
		return m, nil
	}

	// The slash-command menu, when open, owns navigation/accept keys.
	if len(m.menu) > 0 {
		if handled, model, cmd := m.onMenuKey(msg); handled {
			return model, cmd
		}
	}
	// The @-mention file picker, when open, owns the same keys.
	if len(m.atItems) > 0 {
		if handled, model, cmd := m.onAtKey(msg); handled {
			return model, cmd
		}
	}

	switch msg.String() {
	case "ctrl+f":
		return m.enterSearch()
	case "up":
		if s, ok := m.inputHist.prev(); ok {
			m.input.SetValue(s)
			m.input.CursorEnd()
		}
		return m, nil
	case "down":
		if s, ok := m.inputHist.next(); ok {
			m.input.SetValue(s)
			m.input.CursorEnd()
		}
		return m, nil
	}

	if msg.Type == tea.KeyEnter {
		return m.submit()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateMenu()
	atCmd := m.updateAt()
	return m, tea.Batch(cmd, atCmd)
}

// onMenuKey handles keys while the slash-command menu is open: ↑/↓ (and
// ctrl+p/n) cycle the selection with wrap-around, tab completes the name and
// leaves the cursor ready for arguments, enter accepts and runs it, esc
// dismisses the menu. It reports whether it consumed the key.
func (m Model) onMenuKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	n := len(m.menu)
	switch msg.String() {
	case "up", "ctrl+p":
		m.menuIdx = (m.menuIdx - 1 + n) % n
		return true, m, nil
	case "down", "ctrl+n":
		m.menuIdx = (m.menuIdx + 1) % n
		return true, m, nil
	case "tab":
		m.completeMenu()
		return true, m, nil
	case "enter":
		model, cmd := m.acceptMenu()
		return true, model, cmd
	case "esc":
		m.menuHideFor = m.input.Value()
		m.menu = nil
		m.relayout()
		return true, m, nil
	}
	return false, m, nil
}

// updateMenu recomputes the slash-command suggestions from the current input,
// clamps the selection, and relays out the chrome if the row count changed. The
// menu stays hidden while the input matches the value it was dismissed at.
func (m *Model) updateMenu() {
	if m.commands == nil {
		m.menu = nil
		return
	}
	prev := len(m.menu)
	if v := m.input.Value(); v == m.menuHideFor {
		m.menu = nil
	} else {
		m.menu = commandMenu(m.commands.All(), v)
		m.menuHideFor = ""
	}
	if m.menuIdx >= len(m.menu) {
		m.menuIdx = 0
	}
	if len(m.menu) != prev {
		m.relayout()
	}
}

// completeMenu fills the input with the selected command name and a trailing
// space, ready for arguments; the menu closes (the space ends name-typing).
func (m *Model) completeMenu() {
	m.input.SetValue("/" + m.menu[m.menuIdx].Name() + " ")
	m.input.CursorEnd()
	m.menu = nil
	m.relayout()
}

// acceptMenu fills in the selected command and submits it immediately.
func (m Model) acceptMenu() (tea.Model, tea.Cmd) {
	m.input.SetValue("/" + m.menu[m.menuIdx].Name())
	m.menu = nil
	return m.submit()
}

// handleScroll moves the transcript viewport for the recognized scroll keys and
// reports whether it consumed the key. It deliberately avoids keys the input
// field uses for editing (home/end, ctrl+u, ctrl+a/e, word motions), so only
// page (pgup/pgdown) and line (shift+↑/↓) scrolling are claimed here; the mouse
// wheel scrolls too (handled in Update). It takes a pointer so the scroll
// position sticks.
func (m *Model) handleScroll(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup":
		m.viewport.ViewUp()
	case "pgdown":
		m.viewport.ViewDown()
	case "shift+up":
		m.viewport.LineUp(1)
	case "shift+down":
		m.viewport.LineDown(1)
	default:
		return false
	}
	return true
}

// enterSearch switches the input into transcript-find mode.
func (m Model) enterSearch() (tea.Model, tea.Cmd) {
	m.searching = true
	m.search.clear()
	m.input.Reset()
	m.input.Placeholder = "search transcript…"
	return m, nil
}

// exitSearch leaves find mode and restores the normal prompt.
func (m *Model) exitSearch() {
	m.searching = false
	m.search.clear()
	m.input.Reset()
	m.input.Placeholder = "Ask Korai…"
}

// onSearchKey handles keys while the input is a find box: esc exits, enter and
// ctrl+n/↓ jump to the next match, ctrl+p/↑ to the previous, and any other key
// edits the query and re-runs the search.
func (m Model) onSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.exitSearch()
		return m, nil
	case "enter", "ctrl+n", "down":
		m.search.nextHit()
		m.scrollToMatch()
		return m, nil
	case "ctrl+p", "up":
		m.search.prevHit()
		m.scrollToMatch()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.search.run(m.input.Value(), m.entryTexts())
	m.scrollToMatch()
	return m, cmd
}

// entryTexts returns the raw text of every transcript entry, for searching.
func (m Model) entryTexts() []string {
	texts := make([]string, len(m.entries))
	for i := range m.entries {
		texts[i] = m.entries[i].text
	}
	return texts
}

// scrollToMatch scrolls the viewport so the current search match is in view.
func (m *Model) scrollToMatch() {
	idx, ok := m.search.current()
	if !ok {
		return
	}
	if off := m.entryLineOffset(idx); off >= 0 {
		m.viewport.SetYOffset(off)
	}
}

func (m Model) onDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var (
		decision   perm.Decision
		forSession bool
	)
	switch msg.String() {
	case "y", "Y":
		decision = perm.DecisionAllow
	case "a", "A":
		decision = perm.DecisionAllow
		forSession = true
	case "n", "N", "esc":
		decision = perm.DecisionDeny
	default:
		return m, nil
	}

	pr := *m.pending
	m.pending = nil
	verb := "denied"
	switch {
	case forSession:
		m.sessionAllowed[pr.req.ToolName] = true
		verb = "allowed for session"
	case decision == perm.DecisionAllow:
		verb = "allowed"
	}
	m.addEntry(kindInfo, fmt.Sprintf("%s %s", verb, pr.req.ToolName))
	return m, tea.Batch(replyPermission(pr, decision), waitForPermission(m.asker))
}

// planOptions are the choices in the plan-approval dialog, selected with ↑/↓.
var planOptions = []string{
	"Approve",
	"Approve & accept edits",
	"Keep planning (give feedback)",
}

// onPlanKey drives the plan-approval dialog: ↑/↓ (and ctrl+p/n) move the
// selection, enter activates it, esc keeps planning without feedback.
func (m Model) onPlanKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p":
		m.planChoice = (m.planChoice - 1 + len(planOptions)) % len(planOptions)
		return m, nil
	case "down", "ctrl+n":
		m.planChoice = (m.planChoice + 1) % len(planOptions)
		return m, nil
	case "esc":
		return m.resolvePlan(plantool.Reject, "", "plan rejected — staying in plan mode")
	case "enter":
		switch m.planChoice {
		case 0:
			return m.resolvePlan(plantool.Approve, "", "plan approved — leaving plan mode")
		case 1:
			return m.resolvePlan(plantool.ApproveAcceptEdits, "", "plan approved — accept edits")
		default:
			// Open a feedback box; the rejection is sent when it is submitted.
			m.planFeedback = true
			m.input.Reset()
			m.input.Placeholder = "what to change (enter to send, esc to skip)…"
			return m, nil
		}
	}
	return m, nil
}

// onPlanFeedbackKey handles the "keep planning" feedback box: enter sends the
// feedback with a reject, esc cancels back to the dialog.
func (m Model) onPlanFeedbackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		feedback := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		m.input.Placeholder = "Ask Korai…"
		m.planFeedback = false
		return m.resolvePlan(plantool.Reject, feedback, "plan rejected — revising")
	case "esc":
		m.input.Reset()
		m.input.Placeholder = "Ask Korai…"
		m.planFeedback = false
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// resolvePlan delivers a plan decision, records a note, and re-arms the listener.
func (m Model) resolvePlan(decision plantool.Decision, feedback, note string) (tea.Model, tea.Cmd) {
	pr := *m.pendingPlan
	m.pendingPlan = nil
	m.addEntry(kindInfo, note)
	return m, tea.Batch(replyPlan(pr, decision, feedback), waitForPlan(m.planApprover))
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	line := m.input.Value()
	// A trailing backslash continues the prompt on the next line instead of
	// submitting, so multi-line input is possible without a multiline widget.
	if strings.HasSuffix(line, "\\") {
		m.draft += strings.TrimSuffix(line, "\\") + "\n"
		m.input.Reset()
		m.relayout() // the draft preview grows the chrome
		m.refreshViewport()
		return m, nil
	}

	text := strings.TrimSpace(m.draft + line)
	hadDraft := m.draft != ""
	m.draft = ""
	m.input.Reset()
	m.menu = nil
	m.menuIdx = 0
	m.atItems = nil
	m.atIdx = 0
	if hadDraft {
		m.relayout()
	}
	if text == "" {
		return m, nil
	}
	m.inputHist.add(text)

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
	case command.ResumeSession:
		return m.startResume(res.Text)
	default:
		return m, nil
	}
}

// startResume loads a saved session by id via the injected loader.
func (m Model) startResume(id string) (tea.Model, tea.Cmd) {
	if m.resumeLoader == nil {
		m.addEntry(kindInfo, "resume is unavailable")
		return m, nil
	}
	loader := m.resumeLoader
	return m, func() tea.Msg {
		msgs, created, err := loader(id)
		return resumeLoadedMsg{id: id, created: created, messages: msgs, err: err}
	}
}

// saveCmd persists the current conversation, if a saver is wired.
func (m Model) saveCmd() tea.Cmd {
	if m.saver == nil || m.sessionID == "" {
		return nil
	}
	saver, id, created, history := m.saver, m.sessionID, m.sessionStart, m.history
	return func() tea.Msg {
		saver(id, created, history)
		return nil
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

// startTurn records the prompt and launches an engine turn. The transcript
// shows what the user typed; the message sent to the model has any @-referenced
// files inlined by mentionExpander.
func (m Model) startTurn(promptText string) (tea.Model, tea.Cmd) {
	if !strings.HasPrefix(strings.TrimSpace(promptText), "/") {
		m.addEntry(kindUser, promptText)
	}
	sendText := promptText
	if m.mentionExpander != nil {
		sendText = m.mentionExpander(promptText)
	}
	m.history = append(m.history, apiclient.Message{
		Role:    apiclient.RoleUser,
		Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: sendText}},
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
		m.pendingEdit = parseEditChange(ev.Name, ev.Input)
		m.addEntry(kindTool, toolHeader(ev.Name, ev.Input))
	case engine.ToolResultEvent:
		m.streaming = false
		m.addEntry(kindToolResult, toolSummary(ev.Name, ev.Result))
		// Show a +/- diff under a successful Edit. The before/after text is kept
		// on the entry so the block reflows when the terminal is resized.
		if m.pendingEdit != nil && !ev.Result.IsError {
			if renderDiff(m.pendingEdit.old, m.pendingEdit.new, m.diffWidth()) != "" {
				m.entries = append(m.entries, entry{
					kind:    kindDiff,
					diffOld: m.pendingEdit.old,
					diffNew: m.pendingEdit.new,
				})
				m.refreshViewport()
			}
		}
		m.pendingEdit = nil
	case engine.CompactedEvent:
		m.addEntry(kindInfo, fmt.Sprintf("auto-compacted context: %d → %d messages", ev.Before, ev.After))
	case engine.DoneEvent:
		m.history = ev.Messages
		m.busy = false
		m.streaming = false
		m.refreshViewport()
		return m, m.saveCmd()
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

// parseEditChange extracts the before/after text from an Edit tool's input so a
// diff can be shown. It returns nil for other tools or unparseable input. A
// replace-all edit substitutes every occurrence; a single edit substitutes the
// first, which is what the diff approximates here.
func parseEditChange(name string, input json.RawMessage) *editChange {
	if name != "Edit" {
		return nil
	}
	var in struct {
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil
	}
	if in.OldString == "" && in.NewString == "" {
		return nil
	}
	return &editChange{old: in.OldString, new: in.NewString}
}

// diffWidth is the width available for a diff block, accounting for its indent.
func (m Model) diffWidth() int {
	w := m.viewport.Width - 4
	if w < 20 {
		w = 20
	}
	return w
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

// renderEntries assembles the full transcript and records where each entry
// begins (entryOffsets) so search can scroll to a hit. Assistant text is
// rendered as markdown (cached per entry/width); the entry currently being
// streamed is shown as raw text, since partial markdown is noisy and
// re-rendering every token is wasteful. Tool calls show a "●" bullet, their
// results a "⎿" connector.
func (m *Model) renderEntries() string {
	if len(m.entries) == 0 {
		return m.welcomeView()
	}
	w := m.viewport.Width
	m.entryOffsets = make([]int, len(m.entries))
	var b strings.Builder
	line := 0
	for i := range m.entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		m.entryOffsets[i] = line
		block := m.renderEntry(i, w)
		b.WriteString(block)
		line += strings.Count(block, "\n") + 1
	}
	return b.String()
}

// renderEntry renders a single transcript entry to its styled block.
func (m *Model) renderEntry(i, w int) string {
	e := &m.entries[i]
	switch e.kind {
	case kindUser:
		return m.styles.user.Width(w).Render("› " + e.text)
	case kindAssistant:
		return m.assistantText(i, w)
	case kindTool:
		return m.styles.tool.Width(w).Render("● " + e.text)
	case kindToolResult:
		return m.styles.toolResult.Width(w).Render("  ⎿ " + e.text)
	case kindDiff:
		// Rendered fresh at the current width so it reflows on resize, then
		// indented under the result it belongs to.
		return indent(renderDiff(e.diffOld, e.diffNew, m.diffWidth()), "    ")
	case kindError:
		return m.styles.errorText.Width(w).Render("✗ " + e.text)
	case kindInfo:
		return m.styles.info.Width(w).Render("• " + e.text)
	}
	return ""
}

// entryLineOffset returns the first rendered line of entry idx, or -1 if it has
// not been laid out yet.
func (m Model) entryLineOffset(idx int) int {
	if idx < 0 || idx >= len(m.entryOffsets) {
		return -1
	}
	return m.entryOffsets[idx]
}

// indent prefixes every line of s with prefix.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// assistantText returns the display form of assistant entry i. The actively
// streaming entry (the last one while m.streaming) is shown raw; finalized
// entries are markdown-rendered and cached at width w.
func (m *Model) assistantText(i, w int) string {
	e := &m.entries[i]
	if m.streaming && i == len(m.entries)-1 {
		return m.styles.assistant.Width(w).Render(e.text)
	}
	if m.md != nil && (e.rendered == "" || e.renderedWidth != w) {
		e.rendered = m.md.render(e.text)
		e.renderedWidth = w
	}
	if e.rendered != "" {
		return e.rendered
	}
	return m.styles.assistant.Width(w).Render(e.text)
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
	case m.pendingPlan != nil && m.planFeedback:
		bottom = m.renderPlanFeedback()
	case m.pendingPlan != nil:
		bottom = m.renderPlanDialog()
	case m.pending != nil:
		bottom = m.renderDialog()
	case m.searching:
		bottom = m.renderSearch()
	case m.busy:
		bottom = m.spinner.View() + " working… (esc to interrupt)"
	default:
		bottom = m.inputView()
	}

	lines := []string{m.viewport.View()}
	if status := m.statusLine(); status != "" {
		lines = append(lines, status)
	}
	if badge := m.modeBadge(); badge != "" {
		lines = append(lines, badge)
	}
	if menu := m.menuView(); menu != "" {
		lines = append(lines, menu)
	}
	if at := m.atMenuView(); at != "" {
		lines = append(lines, at)
	}
	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

// menuView renders the slash-command suggestion list, the selected row
// highlighted. Empty when the menu is closed.
func (m Model) menuView() string {
	items, sel := m.menuWindow()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range items {
		row := fmt.Sprintf("/%-12s %s", c.Name(), c.Description())
		if i == sel {
			b.WriteString(m.styles.menuSel.Render("› " + row))
		} else {
			b.WriteString(m.styles.menuItem.Render("  " + row))
		}
		if i < len(items)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// inputView renders the prompt, any "\"-continued draft lines above it, and a
// dim argument hint when the user is typing a known slash command.
func (m Model) inputView() string {
	v := m.input.View()
	// The argument hint only shows once the command name is complete; while the
	// name is still being typed the dropdown menu covers that.
	if hint := m.argHint(); hint != "" && len(m.menu) == 0 {
		v += "  " + hint
	}
	if m.draft != "" {
		draft := m.styles.status.Render(strings.TrimRight(m.draft, "\n"))
		return draft + "\n" + v
	}
	return v
}

// argHint returns a dim description of the slash command currently being typed,
// shown as ghost text next to the prompt. Empty when the input is not a known
// command.
func (m Model) argHint() string {
	if m.commands == nil {
		return ""
	}
	val := m.input.Value()
	if !strings.HasPrefix(val, "/") {
		return ""
	}
	fields := strings.Fields(strings.TrimPrefix(val, "/"))
	if len(fields) == 0 {
		return ""
	}
	if c, ok := m.commands.Get(fields[0]); ok {
		return m.styles.status.Render("— " + c.Description())
	}
	return ""
}

// renderSearch renders the find box and the current match position.
func (m Model) renderSearch() string {
	hits := m.search.hits()
	pos := 0
	if idx, ok := m.search.current(); ok {
		for i, h := range hits {
			if h == idx {
				pos = i + 1
				break
			}
		}
	}
	box := "find: " + m.input.View()
	meta := m.styles.status.Render(fmt.Sprintf("  %d/%d · enter/↓ next · ↑ prev · esc exit", pos, len(hits)))
	return box + meta
}

// statusLine renders the bottom status: active model and token usage so far.
func (m Model) statusLine() string {
	var parts []string
	if m.models != nil {
		parts = append(parts, m.models.Get())
	}
	if m.cost != nil {
		if in, out := m.cost.Totals(); in > 0 || out > 0 {
			parts = append(parts, fmt.Sprintf("↑%s ↓%s tok", humanCount(in), humanCount(out)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return m.styles.status.Render(strings.Join(parts, " · "))
}

// humanCount formats a token count compactly (1.2k, 3.4M).
func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
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
	const prompt = "[y]es once   [a]lways (session)   [n]o"
	body := fmt.Sprintf("Allow %s?\n%s", pr.req.ToolName, prompt)
	if args := oneLine(string(pr.req.Input)); args != "" {
		body = fmt.Sprintf("Allow %s?\n%s\n%s", pr.req.ToolName, args, prompt)
	}
	return m.styles.dialog.Render(body)
}

// renderPlanDialog shows the proposed plan and the approval options as a
// selectable list (↑/↓ to move, enter to confirm).
func (m Model) renderPlanDialog() string {
	var b strings.Builder
	b.WriteString("Proposed plan:\n\n")
	b.WriteString(strings.TrimSpace(m.pendingPlan.plan))
	b.WriteString("\n\n")
	for i, opt := range planOptions {
		if i == m.planChoice {
			b.WriteString(m.styles.menuSel.Render("› " + opt))
		} else {
			b.WriteString(m.styles.menuItem.Render("  " + opt))
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.styles.status.Render("↑/↓ select · enter confirm · esc keep planning"))
	return m.styles.dialog.Width(m.viewport.Width).Render(b.String())
}

// renderPlanFeedback shows the "keep planning" feedback box.
func (m Model) renderPlanFeedback() string {
	body := "Keep planning — tell the agent what to change:\n\n" + m.input.View() +
		"\n\nenter to send · esc to skip"
	return m.styles.dialog.Width(m.viewport.Width).Render(body)
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
