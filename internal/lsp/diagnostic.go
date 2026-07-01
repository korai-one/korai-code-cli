package lsp

import (
	"fmt"
	"sort"
	"strings"

	protocol "github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// maxBlockLines is the maximum number of diagnostic lines rendered in a single
// block before the remainder is collapsed into a "... and N more" line.
const maxBlockLines = 10

// SeverityLabel returns the human-readable label for an LSP diagnostic
// severity: "Error", "Warning", "Info" or "Hint". Unknown or unset severities
// default to "Error".
func SeverityLabel(s protocol.DiagnosticSeverity) string {
	switch s {
	case protocol.SeverityError:
		return "Error"
	case protocol.SeverityWarning:
		return "Warning"
	case protocol.SeverityInformation:
		return "Info"
	case protocol.SeverityHint:
		return "Hint"
	default:
		return "Error"
	}
}

// severityRank maps a severity to a sort rank so that errors order before
// warnings, then info, then hints. Unknown severities rank as errors to match
// SeverityLabel's default.
func severityRank(s protocol.DiagnosticSeverity) int {
	switch s {
	case protocol.SeverityError:
		return 0
	case protocol.SeverityWarning:
		return 1
	case protocol.SeverityInformation:
		return 2
	case protocol.SeverityHint:
		return 3
	default:
		return 0
	}
}

// codeString renders a diagnostic code to a string when it is a non-nil string
// or number, returning "" otherwise.
func codeString(code interface{}) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		// Numbers (int, int32, int64, float64, …) and other simple scalars.
		return fmt.Sprintf("%v", v)
	}
}

// messageSuffix builds the trailing source/code annotation for a diagnostic,
// e.g. " (typescript: 2304)", " (typescript)", " (2304)" or "".
func messageSuffix(d protocol.Diagnostic) string {
	source := strings.TrimSpace(d.Source)
	code := codeString(d.Code)
	switch {
	case source != "" && code != "":
		return fmt.Sprintf(" (%s: %s)", source, code)
	case source != "":
		return fmt.Sprintf(" (%s)", source)
	case code != "":
		return fmt.Sprintf(" (%s)", code)
	default:
		return ""
	}
}

// collapseMessage flattens any internal newlines (and other whitespace runs) in
// a diagnostic message into single spaces so each diagnostic renders on one
// line.
func collapseMessage(msg string) string {
	return strings.Join(strings.Fields(msg), " ")
}

// FormatDiagnostic renders a single diagnostic as one line of the form
// "Error [12:5] message (source: code)". The position is 1-based; the source
// and code annotations are appended only when present.
func FormatDiagnostic(d protocol.Diagnostic) string {
	return fmt.Sprintf("%s [%d:%d] %s%s",
		SeverityLabel(d.Severity),
		d.Range.Start.Line+1,
		d.Range.Start.Character+1,
		collapseMessage(d.Message),
		messageSuffix(d),
	)
}

// formatProjectLine renders a diagnostic prefixed with its file path for use in
// the project diagnostics block, e.g. "pkg/foo.go:12:5 Error message (source)".
func formatProjectLine(file string, d protocol.Diagnostic) string {
	return fmt.Sprintf("%s:%d:%d %s %s%s",
		file,
		d.Range.Start.Line+1,
		d.Range.Start.Character+1,
		SeverityLabel(d.Severity),
		collapseMessage(d.Message),
		messageSuffix(d),
	)
}

// sortBySeverity returns the diagnostics ordered errors-first (then warnings,
// info, hints), preserving the original order within each severity. The input
// slice is not modified.
func sortBySeverity(diags []protocol.Diagnostic) []protocol.Diagnostic {
	out := make([]protocol.Diagnostic, len(diags))
	copy(out, diags)
	sort.SliceStable(out, func(i, j int) bool {
		return severityRank(out[i].Severity) < severityRank(out[j].Severity)
	})
	return out
}

// capLines renders at most maxBlockLines of the given lines, appending a
// "... and N more" line when the input exceeds the cap.
func capLines(lines []string) []string {
	if len(lines) <= maxBlockLines {
		return lines
	}
	out := make([]string, 0, maxBlockLines+1)
	out = append(out, lines[:maxBlockLines]...)
	out = append(out, fmt.Sprintf("... and %d more", len(lines)-maxBlockLines))
	return out
}

// Report renders the diagnostics for a single file as a <file_diagnostics>
// block. Diagnostics are ordered errors-first (preserving source order within a
// severity) and capped at ten rendered lines, with any remainder collapsed into
// a "... and N more" line. It returns "" when there are no diagnostics.
func Report(file string, diags []protocol.Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}

	sorted := sortBySeverity(diags)
	lines := make([]string, 0, len(sorted))
	for _, d := range sorted {
		lines = append(lines, FormatDiagnostic(d))
	}
	lines = capLines(lines)

	var b strings.Builder
	fmt.Fprintf(&b, "<file_diagnostics file=%q>\n", file)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n</file_diagnostics>")
	return b.String()
}

// ReportProject renders diagnostics aggregated across files as a
// <project_diagnostics> block, skipping the exclude path (which Report already
// covers). Each line is prefixed with its file path; lines are ordered
// errors-first (then by path and position) and capped at ten total, with any
// remainder collapsed into a "... and N more" line. It returns "" when there is
// nothing to report.
func ReportProject(byFile map[string][]protocol.Diagnostic, exclude string) string {
	type entry struct {
		file string
		diag protocol.Diagnostic
	}

	files := make([]string, 0, len(byFile))
	for file := range byFile {
		if file == exclude {
			continue
		}
		files = append(files, file)
	}
	sort.Strings(files)

	var entries []entry
	for _, file := range files {
		for _, d := range byFile[file] {
			entries = append(entries, entry{file: file, diag: d})
		}
	}
	if len(entries) == 0 {
		return ""
	}

	sort.SliceStable(entries, func(i, j int) bool {
		ri, rj := severityRank(entries[i].diag.Severity), severityRank(entries[j].diag.Severity)
		if ri != rj {
			return ri < rj
		}
		if entries[i].file != entries[j].file {
			return entries[i].file < entries[j].file
		}
		si, sj := entries[i].diag.Range.Start, entries[j].diag.Range.Start
		if si.Line != sj.Line {
			return si.Line < sj.Line
		}
		return si.Character < sj.Character
	})

	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, formatProjectLine(e.file, e.diag))
	}
	lines = capLines(lines)

	var b strings.Builder
	b.WriteString("<project_diagnostics>\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n</project_diagnostics>")
	return b.String()
}

// Summary renders a one-line <diagnostic_summary> totalling the errors and
// warnings across all files, e.g. "<diagnostic_summary>3 errors, 1
// warnings</diagnostic_summary>". Zero categories are omitted; it returns ""
// when there are no errors or warnings.
func Summary(byFile map[string][]protocol.Diagnostic) string {
	var errors, warnings int
	for _, diags := range byFile {
		for _, d := range diags {
			switch d.Severity {
			case protocol.SeverityError:
				errors++
			case protocol.SeverityWarning:
				warnings++
			}
		}
	}

	var parts []string
	if errors > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", errors))
	}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", warnings))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("<diagnostic_summary>%s</diagnostic_summary>", strings.Join(parts, ", "))
}
