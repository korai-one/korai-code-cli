// Package condense reduces verbose tool output before it enters the model's
// context, saving tokens without hiding anything from the terminal. It is the
// tool-output analogue of package compact (which summarizes conversations): a
// deterministic, hermetic transform applied to a single tool result.
//
// Two reductions are applied, both loss-marked so the model knows output was
// trimmed and can re-run a narrower command:
//
//   - Adjacent-run dedup: a run of identical consecutive lines collapses to one
//     line annotated with its repeat count ("downloading…  (×42)").
//   - Head/tail truncation: output longer than a line budget keeps its head and
//     tail (where compiler and test-runner summaries live) and replaces the
//     middle with a one-line "N lines omitted" marker.
//
// The Filter only touches a configured set of tools (Bash by default) and never
// enlarges output: if condensing does not shrink the content it returns the
// original unchanged.
package condense

import (
	"fmt"
	"strings"
)

// Default line budgets used when a Config field is left zero. They are
// deliberately generous so only genuinely noisy output is trimmed.
const (
	// DefaultMaxLines is the line count above which output is truncated.
	DefaultMaxLines = 200
	// DefaultHeadLines is how many leading lines truncation keeps.
	DefaultHeadLines = 120
	// DefaultTailLines is how many trailing lines truncation keeps.
	DefaultTailLines = 60
)

// DefaultTools returns the tool names whose output is condensed by default.
// Bash is the noisy outlier; the file/search tools (ReadFile, Grep, Glob) are
// already scoped by the model's request, so trimming them would change meaning.
func DefaultTools() []string { return []string{"Bash"} }

// Config configures a Filter. Zero-valued fields fall back to the Default*
// values; an empty Tools list falls back to DefaultTools.
type Config struct {
	// Tools is the set of tool names whose output is condensed.
	Tools []string
	// MaxLines is the line count above which output is head/tail-truncated.
	MaxLines int
	// HeadLines and TailLines are how many lines to keep at each end when
	// truncating.
	HeadLines int
	TailLines int
}

// Filter condenses tool output. It is immutable after construction and safe for
// concurrent use. Construct one with New.
type Filter struct {
	tools     map[string]bool
	maxLines  int
	headLines int
	tailLines int
}

// New returns a Filter built from cfg, substituting defaults for zero-valued
// fields and an empty tool set.
func New(cfg Config) *Filter {
	tools := cfg.Tools
	if len(tools) == 0 {
		tools = DefaultTools()
	}
	set := make(map[string]bool, len(tools))
	for _, t := range tools {
		set[t] = true
	}
	f := &Filter{
		tools:     set,
		maxLines:  cfg.MaxLines,
		headLines: cfg.HeadLines,
		tailLines: cfg.TailLines,
	}
	if f.maxLines <= 0 {
		f.maxLines = DefaultMaxLines
	}
	if f.headLines <= 0 {
		f.headLines = DefaultHeadLines
	}
	if f.tailLines <= 0 {
		f.tailLines = DefaultTailLines
	}
	return f
}

// Apply returns content condensed for the named tool. It returns content
// unchanged when the tool is not targeted, when content is empty, or when
// condensing would not make it shorter — so a caller can use the result
// unconditionally.
func (f *Filter) Apply(toolName, content string) string {
	if f == nil || !f.tools[toolName] || content == "" {
		return content
	}
	condensed := f.condense(content)
	if len(condensed) >= len(content) {
		return content
	}
	return condensed
}

// Condense applies the reduction to content unconditionally — no tool-name
// gate. It is the entry point for callers that already decided the content is
// old enough to squeeze (the compaction middle tier), where the per-tool gate
// of Apply would wrongly spare non-Bash output. Like Apply, it never enlarges:
// if condensing does not shrink the content the original is returned.
func (f *Filter) Condense(content string) string {
	if f == nil || content == "" {
		return content
	}
	condensed := f.condense(content)
	if len(condensed) >= len(content) {
		return content
	}
	return condensed
}

// condense applies dedup then truncation, preserving a trailing newline so the
// model sees the same line-termination shape it otherwise would.
func (f *Filter) condense(content string) string {
	trailingNL := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	lines = dedupAdjacent(lines)
	lines = f.truncate(lines)

	out := strings.Join(lines, "\n")
	if trailingNL {
		out += "\n"
	}
	return out
}

// dedupAdjacent collapses each run of identical consecutive lines into a single
// line. A run of a non-blank line longer than one is annotated with its count;
// a run of blank lines collapses to one blank line without a marker (a lone
// count on an empty line would only add noise).
func dedupAdjacent(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		n := j - i
		if n > 1 && strings.TrimSpace(lines[i]) != "" {
			out = append(out, fmt.Sprintf("%s  (×%d)", lines[i], n))
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	return out
}

// truncate keeps the head and tail of an over-budget slice, replacing the
// middle with a one-line omission marker. It is a no-op when the slice fits the
// budget or when the head and tail alone would cover it.
func (f *Filter) truncate(lines []string) []string {
	if len(lines) <= f.maxLines || f.headLines+f.tailLines >= len(lines) {
		return lines
	}
	omitted := len(lines) - f.headLines - f.tailLines
	out := make([]string, 0, f.headLines+f.tailLines+1)
	out = append(out, lines[:f.headLines]...)
	out = append(out, fmt.Sprintf(
		"… %d lines omitted (condensed to save tokens; full output shown in the terminal) …",
		omitted))
	out = append(out, lines[len(lines)-f.tailLines:]...)
	return out
}
