package tui

import (
	"strings"

	"charm.land/glamour/v2"
)

// markdownRenderer turns assistant message text into ANSI-styled terminal
// output (headers, bold/italic, lists, fenced code blocks with syntax
// highlighting, blockquotes). It wraps glamour with a word-wrap width matched
// to the viewport so rendered text never overflows.
type markdownRenderer struct {
	tr    *glamour.TermRenderer
	width int
}

// newMarkdownRenderer builds a renderer wrapping at the given width. A width of
// zero or less disables word wrapping. On error (glamour misconfiguration) it
// returns a renderer whose Render falls back to the raw input.
func newMarkdownRenderer(width int) *markdownRenderer {
	if width < 1 {
		width = 80
	}
	tr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return &markdownRenderer{width: width}
	}
	return &markdownRenderer{tr: tr, width: width}
}

// render returns text rendered as markdown. If the renderer is unavailable or
// rendering fails, it returns the input unchanged so output is never lost.
// glamour adds a leading and trailing blank line and a left margin; the result
// is trimmed of surrounding blank lines to sit flush in the transcript.
func (r *markdownRenderer) render(text string) string {
	if r == nil || r.tr == nil {
		return text
	}
	out, err := r.tr.Render(text)
	if err != nil {
		return text
	}
	return strings.Trim(out, "\n")
}
