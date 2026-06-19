package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// toolHeader renders the one-line summary of a tool call shown next to the
// bullet, e.g. "Read(internal/tui/model.go)" or "Bash(go test ./...)". It shows
// the tool name and its most salient argument — never the full input — so the
// transcript reads as a list of actions, not a dump of JSON.
func toolHeader(name string, input json.RawMessage) string {
	arg := toolHeaderArg(name, input)
	if arg == "" {
		return name
	}
	return fmt.Sprintf("%s(%s)", name, arg)
}

// toolHeaderArg extracts the single argument worth showing in the header for a
// given tool. Unknown tools and unparseable input yield an empty argument, so
// the header degrades to just the tool name.
func toolHeaderArg(name string, input json.RawMessage) string {
	var fields struct {
		Path        string `json:"path"`
		Command     string `json:"command"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
		Note        string `json:"note"`
	}
	_ = json.Unmarshal(input, &fields)

	switch name {
	case "ReadFile", "Write", "Edit":
		return fields.Path
	case "Bash":
		return truncate(oneLine(fields.Command), 60)
	case "Grep", "Glob":
		return fields.Pattern
	case "WebFetch":
		return fields.URL
	case "WebSearch":
		return truncate(fields.Query, 60)
	case "Task":
		return truncate(fields.Description, 60)
	case "Remember":
		return truncate(oneLine(fields.Note), 60)
	default:
		return ""
	}
}

// toolSummary renders the result line shown under a tool call (after the ⎿
// prefix). It is a short outcome — "Read 42 lines", "Found 3 matches",
// "Updated main.go" — not the tool's full output, which stays in the model's
// context but never floods the transcript. Errors show their message instead.
func toolSummary(name string, r tool.Result) string {
	if r.IsError {
		return "error: " + truncate(oneLine(r.Content), 120)
	}
	content := r.Content
	switch name {
	case "ReadFile":
		return fmt.Sprintf("Read %s", countLines(content))
	case "Write":
		return "Wrote file"
	case "Edit":
		return "Updated file"
	case "Grep":
		n := nonEmptyLines(content)
		return fmt.Sprintf("Found %s", plural(n, "match", "matches"))
	case "Glob":
		n := nonEmptyLines(content)
		return fmt.Sprintf("Found %s", plural(n, "file", "files"))
	case "Bash":
		out := strings.TrimSpace(content)
		if out == "" {
			return "(no output)"
		}
		return fmt.Sprintf("%s of output", plural(nonEmptyLines(content), "line", "lines"))
	case "WebFetch", "WebSearch", "Task", "Remember":
		return truncate(oneLine(content), 120)
	default:
		return truncate(oneLine(content), 120)
	}
}

// countLines describes the size of file content as "N lines".
func countLines(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0 lines"
	}
	return plural(strings.Count(s, "\n")+1, "line", "lines")
}

// nonEmptyLines counts the non-blank lines in s, used for match/file tallies.
func nonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// plural formats a count with the singular or plural noun, e.g. "1 line",
// "3 lines".
func plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// truncate shortens s to at most max runes, appending an ellipsis when cut.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
