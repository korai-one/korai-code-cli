package tui

// inputHistory stores submitted prompt lines for Up/Down recall, modeling the
// behavior of a shell's command history. Entries are kept oldest-first. A
// navigation cursor (pos) tracks the user's position while scrolling with the
// arrow keys; it is reset whenever a new entry is added or reset is called.
type inputHistory struct {
	// entries holds submitted lines, oldest first, newest last.
	entries []string
	// pos is the current navigation cursor. It is len(entries) when not
	// navigating (i.e. sitting on the blank draft line), and an index into
	// entries while scrolling backward through history.
	pos int
}

// add records a submitted entry. Blank entries and entries identical to the
// most recent one are ignored (no adjacent duplicates). Adding resets any
// in-progress navigation so the next prev starts from the newest entry.
func (h *inputHistory) add(s string) {
	if s != "" && (len(h.entries) == 0 || h.entries[len(h.entries)-1] != s) {
		h.entries = append(h.entries, s)
	}
	h.reset()
}

// prev returns the previous (older) entry while navigating backward through
// history, and true. At the oldest entry it stays there. It returns ("", false)
// if there is no history. The first prev after add or reset returns the newest
// entry.
func (h *inputHistory) prev() (string, bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if h.pos > 0 {
		h.pos--
	}
	return h.entries[h.pos], true
}

// next returns the next (newer) entry, and true. Moving past the newest entry
// returns the empty draft ("", true) so the user lands back on a blank prompt.
// It returns ("", false) when not currently navigating.
func (h *inputHistory) next() (string, bool) {
	if h.pos >= len(h.entries) {
		return "", false
	}
	h.pos++
	if h.pos >= len(h.entries) {
		return "", true
	}
	return h.entries[h.pos], true
}

// reset abandons navigation; the next prev starts from the newest entry again.
func (h *inputHistory) reset() {
	h.pos = len(h.entries)
}
