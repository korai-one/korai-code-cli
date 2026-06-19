package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

// maxFileMatches caps the number of @-mention file suggestions surfaced at once.
const maxFileMatches = 10

// atToken returns the file-mention token under the cursor: the text after an "@"
// that begins at the start of input or just after whitespace and runs up to the
// cursor with no intervening space. ok is false when the cursor is not inside
// such a token. start is the rune index of the "@" (textinput positions are rune
// indices, so the scan works in runes to stay correct with multibyte input).
func (m Model) atToken() (start int, query string, ok bool) {
	r := []rune(m.input.Value())
	cur := m.input.Position()
	if cur > len(r) {
		cur = len(r)
	}
	for i := cur - 1; i >= 0; i-- {
		switch r[i] {
		case '@':
			if i == 0 || r[i-1] == ' ' || r[i-1] == '\t' {
				return i, string(r[i+1 : cur]), true
			}
			return 0, "", false
		case ' ', '\t':
			return 0, "", false
		}
	}
	return 0, "", false
}

// filterFiles returns up to maxFileMatches workspace paths matching query. An
// empty query yields the head of the (already sorted) candidate list; otherwise
// results are fuzzy-ranked best-first.
func filterFiles(files []string, query string) []string {
	if query == "" {
		if len(files) > maxFileMatches {
			return files[:maxFileMatches]
		}
		return files
	}
	matches := fuzzy.Find(query, files)
	limit := len(matches)
	if limit > maxFileMatches {
		limit = maxFileMatches
	}
	out := make([]string, limit)
	for i := 0; i < limit; i++ {
		out[i] = files[matches[i].Index]
	}
	return out
}

// updateAt recomputes the @-mention suggestions from the current input. It loads
// the workspace file list lazily (via a tea.Cmd) the first time a mention is
// started, and returns that command so the list arrives as a filesLoadedMsg.
func (m *Model) updateAt() tea.Cmd {
	if m.fileFinder == nil {
		m.atItems = nil
		return nil
	}
	_, q, ok := m.atToken()
	if !ok || m.input.Value() == m.atHideFor {
		if m.atItems != nil {
			m.atItems = nil
			m.relayout()
		}
		return nil
	}
	m.atHideFor = ""
	if !m.filesLoaded {
		if m.filesLoading {
			return nil
		}
		m.filesLoading = true
		finder := m.fileFinder
		return func() tea.Msg { return filesLoadedMsg{paths: finder()} }
	}
	prev := len(m.atItems)
	m.atItems = filterFiles(m.files, q)
	if m.atIdx >= len(m.atItems) {
		m.atIdx = 0
	}
	if len(m.atItems) != prev {
		m.relayout()
	}
	return nil
}

// atWindow returns the visible slice of file suggestions and the selected index
// within it, or (nil, -1) when empty.
func (m Model) atWindow() ([]string, int) {
	n := len(m.atItems)
	if n == 0 {
		return nil, -1
	}
	start, count := m.windowBounds(n, m.atIdx)
	return m.atItems[start : start+count], m.atIdx - start
}

// onAtKey handles keys while the @-mention picker is open: ↑/↓ (and ctrl+p/n)
// cycle with wrap, tab/enter insert the selected path, esc dismisses. It reports
// whether it consumed the key.
func (m Model) onAtKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	n := len(m.atItems)
	switch msg.String() {
	case "up", "ctrl+p":
		m.atIdx = (m.atIdx - 1 + n) % n
		return true, m, nil
	case "down", "ctrl+n":
		m.atIdx = (m.atIdx + 1) % n
		return true, m, nil
	case "tab", "enter":
		m.acceptAt()
		return true, m, nil
	case "esc":
		m.atHideFor = m.input.Value()
		m.atItems = nil
		m.relayout()
		return true, m, nil
	}
	return false, m, nil
}

// acceptAt replaces the in-progress @token with the selected path and a trailing
// space, leaving the cursor after it so another mention or text can follow.
// Positions are rune indices to stay correct with multibyte input.
func (m *Model) acceptAt() {
	start, _, ok := m.atToken()
	if !ok {
		return
	}
	r := []rune(m.input.Value())
	cur := m.input.Position()
	if cur > len(r) {
		cur = len(r)
	}
	insert := []rune("@" + m.atItems[m.atIdx] + " ")
	next := make([]rune, 0, len(r)+len(insert))
	next = append(next, r[:start]...)
	next = append(next, insert...)
	next = append(next, r[cur:]...)
	m.input.SetValue(string(next))
	m.input.SetCursor(start + len(insert))
	m.atItems = nil
	m.atIdx = 0
	m.relayout()
}

// atMenuView renders the @-mention file picker, the selected row highlighted.
func (m Model) atMenuView() string {
	items, sel := m.atWindow()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for i, path := range items {
		row := "@" + path
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
