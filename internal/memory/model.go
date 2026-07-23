package memory

import (
	"fmt"
	"strconv"
	"strings"
)

// Fact is a "key: value" memory entry. Pinned facts (and facts without
// keywords) are always injected; a fact with keywords and Pinned=false is
// injected only when one of its keywords matches the user's message.
type Fact struct {
	Key      string
	Value    string
	Pinned   bool
	Keywords []string
}

// gated reports whether the fact is keyword-gated (injected only on a keyword
// match) rather than always injected.
func (f Fact) gated() bool { return len(f.Keywords) > 0 && !f.Pinned }

// Note is a free-text memory entry. Pinned notes are always injected (this is
// how legacy flat-file lines migrate); other notes surface through lexical
// recall. Uses counts how many times recall injected the note — the utility
// signal for eviction.
type Note struct {
	Text   string
	Tags   []string
	Pinned bool
	Uses   int
}

// File is the parsed form of a memory file.
type File struct {
	Facts []Fact
	Notes []Note
}

// Empty reports whether the file holds no entries at all.
func (f File) Empty() bool { return len(f.Facts) == 0 && len(f.Notes) == 0 }

// Section headings of the canonical file format. Parse also accepts the
// French "## Faits" so a hand-edited file in either language loads.
const (
	factsHeading = "## Facts"
	notesHeading = "## Notes"
)

// Parse reads a memory file into its structured form. It never fails: lines it
// cannot interpret degrade to pinned notes so no hand-written content is ever
// dropped. A legacy file (no section headings) parses every line as a pinned
// note, preserving the old always-injected behavior.
func Parse(content string) File {
	const (
		sectionLegacy = iota
		sectionFacts
		sectionNotes
	)
	var f File
	section := sectionLegacy
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			switch strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## "))) {
			case "facts", "faits":
				section = sectionFacts
			case "notes":
				section = sectionNotes
			default:
				// Unknown heading: its content degrades to pinned notes below.
				section = sectionLegacy
			}
			continue
		}
		if strings.HasPrefix(line, "# ") {
			continue // file title
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if line == "" {
			continue
		}
		body, ann := splitAnnotations(line)
		switch section {
		case sectionFacts:
			if key, value, ok := strings.Cut(body, ":"); ok && strings.TrimSpace(key) != "" {
				f.Facts = append(f.Facts, Fact{
					Key:      strings.TrimSpace(key),
					Value:    strings.TrimSpace(value),
					Pinned:   ann.pinned,
					Keywords: ann.keywords,
				})
				continue
			}
			// Not a "key: value" line — keep it as a pinned note.
			f.Notes = append(f.Notes, Note{Text: body, Pinned: true})
		case sectionNotes:
			f.Notes = append(f.Notes, Note{Text: body, Tags: ann.tags, Pinned: ann.pinned, Uses: ann.uses})
		default:
			// Legacy or unknown-section line: always injected, annotations kept
			// verbatim as text (the legacy format had none).
			f.Notes = append(f.Notes, Note{Text: line, Pinned: true})
		}
	}
	return f
}

// Marshal serializes the file into its canonical markdown form (the inverse of
// Parse up to whitespace normalization).
func (f File) Marshal() string {
	if f.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Memory\n")
	if len(f.Facts) > 0 {
		b.WriteString("\n" + factsHeading + "\n\n")
		for _, fa := range f.Facts {
			b.WriteString("- ")
			b.WriteString(fa.Key)
			b.WriteString(": ")
			b.WriteString(fa.Value)
			if fa.Pinned {
				b.WriteString(" [pinned]")
			}
			if len(fa.Keywords) > 0 {
				b.WriteString(" [keywords: " + strings.Join(fa.Keywords, ", ") + "]")
			}
			b.WriteByte('\n')
		}
	}
	if len(f.Notes) > 0 {
		b.WriteString("\n" + notesHeading + "\n\n")
		for _, n := range f.Notes {
			b.WriteString("- ")
			b.WriteString(n.Text)
			if n.Pinned {
				b.WriteString(" [pinned]")
			}
			if len(n.Tags) > 0 {
				b.WriteString(" [tags: " + strings.Join(n.Tags, ", ") + "]")
			}
			if n.Uses > 0 {
				b.WriteString(" [uses: " + strconv.Itoa(n.Uses) + "]")
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// annotations are the recognized trailing "[...]" markers on an entry line.
type annotations struct {
	pinned   bool
	keywords []string
	tags     []string
	uses     int
}

// splitAnnotations strips recognized trailing "[...]" groups from line and
// returns the remaining body plus the parsed annotations. Unrecognized
// bracketed text stays in the body, so a note may legitimately contain
// brackets.
func splitAnnotations(line string) (string, annotations) {
	var ann annotations
	for {
		trimmed := strings.TrimRight(line, " \t")
		if !strings.HasSuffix(trimmed, "]") {
			return trimmed, ann
		}
		open := strings.LastIndex(trimmed, "[")
		if open < 0 {
			return trimmed, ann
		}
		inner := trimmed[open+1 : len(trimmed)-1]
		key, value, _ := strings.Cut(inner, ":")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "pinned":
			if strings.TrimSpace(value) != "" {
				return trimmed, ann // "[pinned: …]" is not a marker
			}
			ann.pinned = true
		case "keywords":
			ann.keywords = splitList(value)
		case "tags":
			ann.tags = splitList(value)
		case "uses":
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return trimmed, ann
			}
			ann.uses = n
		default:
			return trimmed, ann
		}
		line = trimmed[:open]
	}
}

// splitList parses a comma-separated annotation value into trimmed non-empty
// elements.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// String renders a one-line human summary, useful in logs.
func (f File) String() string {
	return fmt.Sprintf("memory(%d facts, %d notes)", len(f.Facts), len(f.Notes))
}
