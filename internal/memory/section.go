package memory

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// section.go renders the per-turn "# Memory" system-prompt section and hosts
// the Provider the engine's dynamic system-section seam calls each request.
//
// Rendered shape (French section names, per the repo's user-facing-language
// convention; sections are omitted when empty):
//
//	# Memory
//
//	## Faits
//	- key: value
//
//	## Notes
//	- pinned note text
//
//	## Rappels
//	- recalled note [tags: …]
//	- … autres notes : « pointer preview » · « pointer preview »

// Per-section character budgets, in the spirit of the reference agent's
// 512/400/300-token sections (≈ tokens × 4 chars). Entries that do not fit are
// dropped whole, never truncated mid-line (except pointer previews, which are
// previews by definition).
const (
	// FactsBudgetChars caps the "## Faits" section (≈ 512 tokens).
	FactsBudgetChars = 2048
	// RecallBudgetChars caps the "## Rappels" section (≈ 400 tokens).
	RecallBudgetChars = 1600
	// NotesBudgetChars caps the pinned-"## Notes" section (≈ 300 tokens).
	NotesBudgetChars = 1200

	// RecallTopK is how many recalled notes are injected in full.
	RecallTopK = 3
	// RecallPointerMax is how many further matches surface as one-line pointers.
	RecallPointerMax = 3
	// pointerPreviewChars is the per-pointer preview length.
	pointerPreviewChars = 60
)

// RenderSection renders the memory context section for the given parsed file
// and latest user message. It returns the section text ("" when nothing is
// injected) and the recalled notes (so the caller can record their use).
func RenderSection(f File, latestUser string) (string, []Note) {
	facts := renderFacts(f.Facts, latestUser)
	pinned := renderPinnedNotes(f.Notes)
	recallSection, recalled := renderRecall(f.Notes, latestUser)

	var sections []string
	if facts != "" {
		sections = append(sections, "## Faits\n"+facts)
	}
	if pinned != "" {
		sections = append(sections, "## Notes\n"+pinned)
	}
	if recallSection != "" {
		sections = append(sections, "## Rappels\n"+recallSection)
	}
	if len(sections) == 0 {
		return "", nil
	}
	return "# Memory\n\n" + strings.Join(sections, "\n\n"), recalled
}

// renderFacts renders the always-injected facts (pinned or keyword-free) plus
// any keyword-gated fact whose keyword matches the user message, sorted by key
// for byte stability, clipped to FactsBudgetChars.
func renderFacts(facts []Fact, latestUser string) string {
	var visible []Fact
	for _, f := range facts {
		if !f.gated() {
			visible = append(visible, f)
			continue
		}
		for _, kw := range f.Keywords {
			if matchesKeyword(latestUser, kw) {
				visible = append(visible, f)
				break
			}
		}
	}
	sort.Slice(visible, func(a, b int) bool { return visible[a].Key < visible[b].Key })
	lines := make([]string, 0, len(visible))
	for _, f := range visible {
		lines = append(lines, "- "+f.Key+": "+f.Value)
	}
	return clipLines(lines, FactsBudgetChars)
}

// renderPinnedNotes renders the always-injected pinned notes in file order,
// clipped to NotesBudgetChars.
func renderPinnedNotes(notes []Note) string {
	var lines []string
	for _, n := range notes {
		if n.Pinned {
			lines = append(lines, "- "+n.Text)
		}
	}
	return clipLines(lines, NotesBudgetChars)
}

// renderRecall renders the top-K recalled notes in full plus a single pointer
// line previewing the next few matches, clipped to RecallBudgetChars. It
// returns the notes injected in full so their use can be recorded.
func renderRecall(notes []Note, latestUser string) (string, []Note) {
	matches := recallNotes(notes, latestUser)
	if len(matches) == 0 {
		return "", nil
	}
	full := matches
	if len(full) > RecallTopK {
		full = full[:RecallTopK]
	}
	var lines []string
	for _, n := range full {
		line := "- " + n.Text
		if len(n.Tags) > 0 {
			line += " [tags: " + strings.Join(n.Tags, ", ") + "]"
		}
		lines = append(lines, line)
	}
	if rest := matches[len(full):]; len(rest) > 0 {
		if len(rest) > RecallPointerMax {
			rest = rest[:RecallPointerMax]
		}
		previews := make([]string, 0, len(rest))
		for _, n := range rest {
			previews = append(previews, "« "+preview(n.Text, pointerPreviewChars)+" »")
		}
		lines = append(lines, "- … autres notes : "+strings.Join(previews, " · "))
	}
	return clipLines(lines, RecallBudgetChars), full
}

// clipLines joins lines, dropping trailing entries once budget chars are
// exceeded. It always keeps at least one line so a single oversize entry is
// injected rather than silently vanishing.
func clipLines(lines []string, budget int) string {
	if len(lines) == 0 {
		return ""
	}
	total := 0
	kept := 0
	for _, l := range lines {
		total += len(l) + 1
		if kept > 0 && total > budget {
			break
		}
		kept++
	}
	return strings.Join(lines[:kept], "\n")
}

// preview clips s to at most n runes, appending an ellipsis when truncated.
func preview(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// Provider serves the per-turn memory section. It re-reads the store on every
// call, so facts and notes recorded mid-session (or edited by hand) become
// visible on the very next model request — the store's file is the single
// source of truth. Recall uses are recorded once per distinct query, not once
// per request, so the tool-loop's repeated request builds do not inflate the
// utility counters.
type Provider struct {
	store *Store

	mu        sync.Mutex
	lastQuery string
}

// NewProvider returns a Provider over store.
func NewProvider(store *Store) *Provider {
	return &Provider{store: store}
}

// Section renders the memory section for the latest user message. It is safe
// for concurrent use and never fails: a store read error is logged and yields
// an empty section (the turn proceeds without memory).
func (p *Provider) Section(latestUser string) string {
	f, err := p.store.Load()
	if err != nil {
		slog.Warn("memory: loading store for injection", "error", err)
		return ""
	}
	if f.Empty() {
		return ""
	}
	section, recalled := RenderSection(f, latestUser)

	p.mu.Lock()
	newQuery := latestUser != p.lastQuery
	if newQuery {
		p.lastQuery = latestUser
	}
	p.mu.Unlock()
	if newQuery && len(recalled) > 0 {
		texts := make([]string, len(recalled))
		for i, n := range recalled {
			texts[i] = n.Text
		}
		if err := p.store.RecordUses(texts); err != nil {
			slog.Warn("memory: recording recalls", "error", err)
		}
	}
	return section
}
