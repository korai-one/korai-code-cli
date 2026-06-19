package tui

import (
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

// maxMenuRows is the most slash-command suggestions shown at once; longer lists
// scroll a window centered on the selection.
const maxMenuRows = 6

// commandMenu returns the slash-command suggestions for the current input. It
// is non-empty only while the user is typing the command name itself — input
// must begin with "/" and contain no space yet (once an argument is typed the
// menu gives way to the argument hint). "/" alone lists every command in
// registry (alphabetical) order; otherwise the typed text is fuzzy-matched
// against command names and the results are ordered best-match first.
func commandMenu(all []command.Command, input string) []command.Command {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	body := input[1:]
	if strings.ContainsAny(body, " \t") {
		return nil
	}
	if body == "" {
		return all
	}
	names := make([]string, len(all))
	for i, c := range all {
		names[i] = c.Name()
	}
	matches := fuzzy.Find(body, names)
	out := make([]command.Command, len(matches))
	for i, mt := range matches {
		out[i] = all[mt.Index]
	}
	return out
}

// menuWindow returns the slice of suggestions currently visible and the index of
// the selected item within that slice, scrolling a fixed-height window that
// keeps the selection centered. It returns (nil, -1) when the menu is empty.
func (m Model) menuWindow() ([]command.Command, int) {
	n := len(m.menu)
	if n == 0 {
		return nil, -1
	}
	rows := maxMenuRows
	// Leave room for the transcript on short terminals.
	if lim := m.height - 3; lim < rows {
		rows = lim
	}
	if rows < 1 {
		rows = 1
	}
	if n <= rows {
		return m.menu, m.menuIdx
	}
	start := m.menuIdx - rows/2
	if start < 0 {
		start = 0
	}
	if start > n-rows {
		start = n - rows
	}
	return m.menu[start : start+rows], m.menuIdx - start
}
