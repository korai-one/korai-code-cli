package tui

import "strings"

// transcriptSearch holds incremental-search state over transcript lines.
type transcriptSearch struct {
	q   string
	idx []int
	cur int
}

// run sets the query and recomputes case-insensitive substring matches against
// texts, returning the indices into texts that match. A blank query clears
// matches. After run, the current match resets to the first match (if any).
func (s *transcriptSearch) run(query string, texts []string) {
	s.q = query
	s.idx = nil
	s.cur = 0
	if query == "" {
		return
	}
	needle := strings.ToLower(query)
	for i, text := range texts {
		if strings.Contains(strings.ToLower(text), needle) {
			s.idx = append(s.idx, i)
		}
	}
}

// query returns the current query string.
func (s *transcriptSearch) query() string {
	return s.q
}

// hits returns the matching indices into the texts slice, in order.
func (s *transcriptSearch) hits() []int {
	return s.idx
}

// current returns the index (into texts) of the currently-focused match and
// true, or (0, false) when there are no matches.
func (s *transcriptSearch) current() (int, bool) {
	if len(s.idx) == 0 {
		return 0, false
	}
	return s.idx[s.cur], true
}

// nextHit advances to the next match, wrapping to the first. No-op if empty.
func (s *transcriptSearch) nextHit() {
	if len(s.idx) == 0 {
		return
	}
	s.cur = (s.cur + 1) % len(s.idx)
}

// prevHit moves to the previous match, wrapping to the last. No-op if empty.
func (s *transcriptSearch) prevHit() {
	if len(s.idx) == 0 {
		return
	}
	s.cur = (s.cur - 1 + len(s.idx)) % len(s.idx)
}

// clear resets the search to empty (no query, no matches).
func (s *transcriptSearch) clear() {
	s.q = ""
	s.idx = nil
	s.cur = 0
}
