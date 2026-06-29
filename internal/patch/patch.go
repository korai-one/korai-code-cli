// Package patch implements the codex apply_patch format: parsing a multi-file
// patch envelope and applying it to in-memory file contents with fuzzy context
// matching.
//
// The package performs NO disk I/O. The caller is responsible for reading the
// current content of the files reported by [Patch.Files] and for writing the
// resolved [Result] values back. This keeps the package pure and testable and
// lets the tool layer gate filesystem permissions.
//
// Line model: the package works with '\n'-separated lines. On apply, CRLF in
// the supplied content is normalized to LF and a missing trailing newline is
// tolerated; produced content always ends with a single trailing newline.
package patch

import (
	"fmt"
	"strings"
)

// Op is the kind of change for a file.
type Op int

const (
	// OpAdd creates a new file.
	OpAdd Op = iota
	// OpUpdate edits an existing file (optionally renaming it).
	OpUpdate
	// OpDelete removes an existing file.
	OpDelete
)

// envelope markers and line prefixes for the apply_patch format.
const (
	beginMarker      = "*** Begin Patch"
	endMarker        = "*** End Patch"
	addFileMarker    = "*** Add File: "
	deleteFileMarker = "*** Delete File: "
	updateFileMarker = "*** Update File: "
	moveToMarker     = "*** Move to: "
	eofMarker        = "*** End of File"
	contextMarker    = "@@"
)

// chunk is one contiguous change region within an Update hunk. It is located in
// the source file by an optional context anchor followed by the old lines.
type chunk struct {
	// context is the optional "@@ heading" anchor used to narrow the search.
	context string
	// hasContext reports whether an "@@" header was present for this chunk.
	hasContext bool
	// oldLines is the contiguous block of lines to replace (context + removed).
	oldLines []string
	// newLines is the replacement block (context + added).
	newLines []string
	// isEOF reports that the block must be located at the end of the file.
	isEOF bool
}

// hunk is a single file operation parsed from the envelope.
type hunk struct {
	op         Op
	path       string
	movePath   string
	addContent string
	chunks     []chunk
}

// Patch is a parsed apply_patch envelope. Construct it with [Parse] and resolve
// it with [Patch.Apply].
type Patch struct {
	hunks []hunk
}

// Result is one resolved file change. For OpDelete, Content is "". For OpUpdate
// with a move, Path is the NEW path and OldPath is the original (empty if no
// move).
type Result struct {
	Path    string
	OldPath string
	Op      Op
	Content string
}

// Parse parses a complete patch envelope. It returns an error with a clear,
// model-facing message on malformed input.
func Parse(text string) (*Patch, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")

	beginIdx, endIdx := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case beginMarker:
			if beginIdx == -1 {
				beginIdx = i
			}
		case endMarker:
			endIdx = i
		}
	}
	if beginIdx == -1 {
		return nil, fmt.Errorf("invalid patch: the first line of the patch must be %q", beginMarker)
	}
	if endIdx == -1 || endIdx <= beginIdx {
		return nil, fmt.Errorf("invalid patch: the last line of the patch must be %q", endMarker)
	}

	body := lines[beginIdx+1 : endIdx]
	hunks, err := parseHunks(body)
	if err != nil {
		return nil, err
	}
	return &Patch{hunks: hunks}, nil
}

// parseHunks walks the lines between the Begin/End markers and builds the list
// of file operations.
func parseHunks(lines []string) ([]hunk, error) {
	var hunks []hunk
	i := 0
	for i < len(lines) {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, addFileMarker):
			path := strings.TrimSpace(strings.TrimPrefix(line, addFileMarker))
			if path == "" {
				return nil, fmt.Errorf("invalid patch: add file hunk is missing a path")
			}
			content, next := parseAddContent(lines, i+1)
			hunks = append(hunks, hunk{op: OpAdd, path: path, addContent: content})
			i = next

		case strings.HasPrefix(line, deleteFileMarker):
			path := strings.TrimSpace(strings.TrimPrefix(line, deleteFileMarker))
			if path == "" {
				return nil, fmt.Errorf("invalid patch: delete file hunk is missing a path")
			}
			hunks = append(hunks, hunk{op: OpDelete, path: path})
			i++

		case strings.HasPrefix(line, updateFileMarker):
			path := strings.TrimSpace(strings.TrimPrefix(line, updateFileMarker))
			if path == "" {
				return nil, fmt.Errorf("invalid patch: update file hunk is missing a path")
			}
			i++
			var movePath string
			if i < len(lines) && strings.HasPrefix(lines[i], moveToMarker) {
				movePath = strings.TrimSpace(strings.TrimPrefix(lines[i], moveToMarker))
				i++
			}
			chunks, next := parseUpdateChunks(lines, i)
			if len(chunks) == 0 {
				return nil, fmt.Errorf("invalid patch: update file hunk for %q is empty", path)
			}
			hunks = append(hunks, hunk{op: OpUpdate, path: path, movePath: movePath, chunks: chunks})
			i = next

		default:
			// Skip stray lines (e.g. an environment-id preamble or blank lines).
			i++
		}
	}
	return hunks, nil
}

// parseAddContent accumulates the "+"-prefixed lines of an Add hunk. The
// returned content carries a trailing newline when non-empty, matching the
// codex convention.
func parseAddContent(lines []string, start int) (string, int) {
	var b strings.Builder
	i := start
	for i < len(lines) && !strings.HasPrefix(lines[i], "***") {
		if strings.HasPrefix(lines[i], "+") {
			b.WriteString(lines[i][1:])
			b.WriteByte('\n')
		}
		i++
	}
	return b.String(), i
}

// parseUpdateChunks parses the change body of an Update hunk into ordered
// chunks. A chunk begins at an "@@" header or, when none has appeared yet, at
// the first context/removal/addition line.
func parseUpdateChunks(lines []string, start int) ([]chunk, int) {
	var chunks []chunk
	var cur *chunk
	i := start

	flush := func() {
		if cur != nil {
			chunks = append(chunks, *cur)
			cur = nil
		}
	}

	for i < len(lines) && !strings.HasPrefix(lines[i], "***") {
		line := lines[i]
		if strings.HasPrefix(line, contextMarker) {
			flush()
			heading := strings.TrimSpace(line[len(contextMarker):])
			cur = &chunk{context: heading, hasContext: true}
			i++
			continue
		}

		// A change line implicitly opens a context-less chunk.
		switch {
		case strings.HasPrefix(line, " "):
			if cur == nil {
				cur = &chunk{}
			}
			cur.oldLines = append(cur.oldLines, line[1:])
			cur.newLines = append(cur.newLines, line[1:])
		case strings.HasPrefix(line, "-"):
			if cur == nil {
				cur = &chunk{}
			}
			cur.oldLines = append(cur.oldLines, line[1:])
		case strings.HasPrefix(line, "+"):
			if cur == nil {
				cur = &chunk{}
			}
			cur.newLines = append(cur.newLines, line[1:])
		default:
			// Ignore unrecognized lines (defensive; matches reference leniency).
		}
		i++
	}
	flush()

	// The "*** End of File" marker, if present, terminated the body; mark the
	// final chunk as end-of-file anchored.
	if i < len(lines) && lines[i] == eofMarker && len(chunks) > 0 {
		chunks[len(chunks)-1].isEOF = true
		i++
	}
	return chunks, i
}

// Files returns the paths whose CURRENT content the patch needs (Update and
// Delete targets). Add targets are excluded. For a renaming Update the original
// (source) path is returned, since that is the file the caller must read.
func (p *Patch) Files() []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, h := range p.hunks {
		if h.op == OpAdd {
			continue
		}
		if _, ok := seen[h.path]; ok {
			continue
		}
		seen[h.path] = struct{}{}
		paths = append(paths, h.path)
	}
	return paths
}

// Apply resolves every file operation against current, a map of path->current
// content for the paths returned by [Patch.Files] (Add targets may be absent).
// It does no I/O. It returns a clear error if a hunk's context cannot be
// located, an Add targets an existing file, or an Update/Delete targets a
// missing one.
func (p *Patch) Apply(current map[string]string) ([]Result, error) {
	results := make([]Result, 0, len(p.hunks))
	for _, h := range p.hunks {
		switch h.op {
		case OpAdd:
			if _, ok := current[h.path]; ok {
				return nil, fmt.Errorf("%s: cannot add file that already exists", h.path)
			}
			results = append(results, Result{Path: h.path, Op: OpAdd, Content: h.addContent})

		case OpDelete:
			if _, ok := current[h.path]; !ok {
				return nil, fmt.Errorf("%s: cannot delete missing file", h.path)
			}
			results = append(results, Result{Path: h.path, Op: OpDelete, Content: ""})

		case OpUpdate:
			content, ok := current[h.path]
			if !ok {
				return nil, fmt.Errorf("%s: cannot update missing file", h.path)
			}
			newContent, err := deriveNewContent(h.path, content, h.chunks)
			if err != nil {
				return nil, err
			}
			res := Result{Path: h.path, Op: OpUpdate, Content: newContent}
			if h.movePath != "" {
				res.Path = h.movePath
				res.OldPath = h.path
			}
			results = append(results, res)
		}
	}
	return results, nil
}

// deriveNewContent applies the chunks to a single file's content and returns the
// resulting text. CRLF is normalized to LF and the output ends with one newline.
func deriveNewContent(path, content string, chunks []chunk) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	// Drop the trailing empty element produced by a final newline so line
	// counts match standard diff behaviour.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	replacements, err := computeReplacements(lines, path, chunks)
	if err != nil {
		return "", err
	}
	newLines := applyReplacements(lines, replacements)

	// Ensure a single trailing newline.
	if len(newLines) == 0 || newLines[len(newLines)-1] != "" {
		newLines = append(newLines, "")
	}
	return strings.Join(newLines, "\n"), nil
}

// replacement records a region to rewrite: starting at start, spanning oldLen
// existing lines, replaced by newLines.
type replacement struct {
	start    int
	oldLen   int
	newLines []string
}

// computeReplacements locates each chunk in order using an advancing cursor and
// the fuzzy [seekSequence] cascade, then returns the replacements sorted by
// position.
func computeReplacements(lines []string, path string, chunks []chunk) ([]replacement, error) {
	var reps []replacement
	cursor := 0

	for _, c := range chunks {
		// Anchor on the "@@" heading, if any, advancing the cursor past it so
		// that identical snippets are disambiguated by position.
		if c.hasContext && c.context != "" {
			idx := seekSequence(lines, []string{c.context}, cursor, false)
			if idx < 0 {
				return nil, fmt.Errorf("%s: failed to find context %q in file", path, c.context)
			}
			cursor = idx + 1
		}

		// Pure addition: insert at end (or just before a trailing blank line).
		if len(c.oldLines) == 0 {
			insertIdx := len(lines)
			if len(lines) > 0 && lines[len(lines)-1] == "" {
				insertIdx = len(lines) - 1
			}
			reps = append(reps, replacement{start: insertIdx, oldLen: 0, newLines: c.newLines})
			continue
		}

		pattern := c.oldLines
		newSlice := c.newLines
		found := seekSequence(lines, pattern, cursor, c.isEOF)

		// Retry without a trailing empty sentinel line, which represents the
		// file's final newline and is absent from our stripped line slice.
		if found < 0 && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
				newSlice = newSlice[:len(newSlice)-1]
			}
			found = seekSequence(lines, pattern, cursor, c.isEOF)
		}

		if found < 0 {
			return nil, fmt.Errorf("%s: failed to find expected lines in file:\n%s",
				path, strings.Join(c.oldLines, "\n"))
		}
		reps = append(reps, replacement{start: found, oldLen: len(pattern), newLines: newSlice})
		cursor = found + len(pattern)
	}

	// Stable sort by start index; chunks already advance the cursor in order.
	for i := 1; i < len(reps); i++ {
		for j := i; j > 0 && reps[j-1].start > reps[j].start; j-- {
			reps[j-1], reps[j] = reps[j], reps[j-1]
		}
	}
	return reps, nil
}

// applyReplacements rewrites lines in reverse position order so that earlier
// edits do not shift the offsets of later ones.
func applyReplacements(lines []string, reps []replacement) []string {
	out := make([]string, len(lines))
	copy(out, lines)

	for i := len(reps) - 1; i >= 0; i-- {
		r := reps[i]
		end := r.start + r.oldLen
		if end > len(out) {
			end = len(out)
		}
		tail := append([]string{}, out[end:]...)
		out = append(out[:r.start], r.newLines...)
		out = append(out, tail...)
	}
	return out
}

// seekSequence finds pattern within lines at or after start, trying matchers of
// decreasing strictness: exact, trailing-whitespace-stripped, fully trimmed,
// and finally with common Unicode punctuation normalized to ASCII. When eof is
// true it first attempts a match anchored at the end of the file. It returns
// the starting index of the match, or -1 if none is found.
func seekSequence(lines, pattern []string, start int, eof bool) int {
	if len(pattern) == 0 {
		return start
	}
	if len(pattern) > len(lines) {
		return -1
	}

	matchers := []func(a, b string) bool{
		func(a, b string) bool { return a == b },
		func(a, b string) bool { return trimEnd(a) == trimEnd(b) },
		func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) },
		func(a, b string) bool { return normalize(a) == normalize(b) },
	}

	for _, eq := range matchers {
		if eof {
			if from := len(lines) - len(pattern); from >= start {
				if matchAt(lines, pattern, from, eq) {
					return from
				}
			}
		}
		for i := start; i <= len(lines)-len(pattern); i++ {
			if matchAt(lines, pattern, i, eq) {
				return i
			}
		}
	}
	return -1
}

// matchAt reports whether pattern matches lines starting at i under eq.
func matchAt(lines, pattern []string, i int, eq func(a, b string) bool) bool {
	for j := range pattern {
		if !eq(lines[i+j], pattern[j]) {
			return false
		}
	}
	return true
}

// trimEnd strips trailing whitespace, including a stray CR from CRLF input.
func trimEnd(s string) string {
	return strings.TrimRight(s, " \t\r\n\v\f")
}

// normalize trims a line and folds common typographic Unicode punctuation to
// ASCII so ASCII-authored patches can match source containing fancy dashes,
// quotes, or exotic spaces.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch r {
		case '‐', '‑', '‒', '–', '—', '―', '−':
			b.WriteByte('-')
		case '‘', '’', '‚', '‛':
			b.WriteByte('\'')
		case '“', '”', '„', '‟':
			b.WriteByte('"')
		case ' ', ' ', ' ', ' ', ' ', ' ',
			' ', ' ', ' ', ' ', ' ', ' ', '　':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
