package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// maxDiffLines is the threshold above which long runs of unchanged context are
// collapsed into a marker to keep large edits readable.
const maxDiffLines = 60

// contextKeep is the number of context lines preserved at each end of a
// collapsed run (so a change keeps a little surrounding context for readability).
const contextKeep = 3

// diffKind classifies a line in a rendered diff.
type diffKind int

const (
	diffContext diffKind = iota // unchanged line
	diffAdd                     // line present only in new
	diffDel                     // line present only in old
	diffMarker                  // collapsed-context marker (rendering only)
)

// diffLine is one line of a computed diff.
type diffLine struct {
	kind diffKind
	text string
}

// computeDiff returns the line-level diff between old and new using a longest-
// common-subsequence alignment: shared lines are diffContext, lines only in old
// are diffDel, lines only in new are diffAdd, in source order.
func computeDiff(old, new string) []diffLine {
	a := strings.Split(old, "\n")
	b := strings.Split(new, "\n")

	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	// Walk the table forward, emitting deletions before additions on divergence
	// so a replacement reads as "- old" then "+ new".
	var out []diffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{kind: diffContext, text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{kind: diffDel, text: a[i]})
			i++
		default:
			out = append(out, diffLine{kind: diffAdd, text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, diffLine{kind: diffDel, text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, diffLine{kind: diffAdd, text: b[j]})
	}
	return out
}

// renderDiff computes and renders the diff between old and new as styled
// terminal text: added lines prefixed "+ " in green, deleted "- " in red,
// context "  " dimmed. Lines are hard-wrapped/truncated to width. When old and
// new are identical it returns "". To keep large edits readable, if there are
// more than maxDiffLines total rendered lines, collapse long runs of context to
// a few lines (showing a "⋯ N unchanged" marker) while keeping every
// added/deleted line.
func renderDiff(old, new string, width int) string {
	if old == new {
		return ""
	}
	lines := computeDiff(old, new)
	if len(lines) > maxDiffLines {
		lines = collapseContext(lines)
	}

	addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	ctxStyle := lipgloss.NewStyle().Faint(true)
	markerStyle := lipgloss.NewStyle().Faint(true)

	var b strings.Builder
	for idx, ln := range lines {
		if idx > 0 {
			b.WriteByte('\n')
		}
		if ln.kind == diffMarker {
			b.WriteString(markerStyle.Render(truncateWidth(ln.text, width)))
			continue
		}
		var prefix string
		var style lipgloss.Style
		switch ln.kind {
		case diffAdd:
			prefix, style = "+ ", addStyle
		case diffDel:
			prefix, style = "- ", delStyle
		default:
			prefix, style = "  ", ctxStyle
		}
		b.WriteString(style.Render(truncateWidth(prefix+ln.text, width)))
	}
	return b.String()
}

// collapseContext replaces long runs of context lines with a single marker line
// ("⋯ N unchanged"), keeping a few context lines at each boundary and every
// added/deleted line untouched.
func collapseContext(lines []diffLine) []diffLine {
	var out []diffLine
	i := 0
	for i < len(lines) {
		if lines[i].kind != diffContext {
			out = append(out, lines[i])
			i++
			continue
		}
		// Gather the full run of context.
		start := i
		for i < len(lines) && lines[i].kind == diffContext {
			i++
		}
		run := lines[start:i]
		// Keep boundary context only when the run is long enough to collapse.
		if len(run) <= 2*contextKeep+1 {
			out = append(out, run...)
			continue
		}
		head := contextKeep
		tail := contextKeep
		// Trim leading context at the very start and trailing at the very end:
		// they carry no neighbouring change, so a single marker suffices.
		if start == 0 {
			head = 0
		}
		if i == len(lines) {
			tail = 0
		}
		out = append(out, run[:head]...)
		hidden := len(run) - head - tail
		out = append(out, diffLine{kind: diffMarker, text: markerText(hidden)})
		out = append(out, run[len(run)-tail:]...)
	}
	return out
}

// markerText formats the collapsed-context marker for n hidden lines.
func markerText(n int) string {
	return "⋯ " + strconv.Itoa(n) + " unchanged"
}

// truncateWidth limits s to at most width display columns (reusing the package
// truncate helper); a non-positive width disables truncation.
func truncateWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	return truncate(s, width)
}
