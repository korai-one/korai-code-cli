package snapshot

import (
	"fmt"
	"strings"
	"sync"
)

// Entry is one labeled snapshot in a session's undo history: the Manager id
// (a tree hash) plus a short human label for the turn the snapshot precedes.
type Entry struct {
	Label string
	ID    string
}

// Log is the in-session, ordered history of snapshots taken before each turn.
// The UI renders it for /snapshots and selects from it for /revert. It is safe
// for concurrent use: snapshots are appended from a background command while the
// UI reads it. It holds no git state — just the (label, id) bookkeeping.
type Log struct {
	mu      sync.Mutex
	entries []Entry
}

// Add appends a snapshot to the history.
func (l *Log) Add(label, id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, Entry{Label: label, ID: id})
}

// Len returns the number of snapshots recorded.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// At returns the entry stepsBack from the end: stepsBack=1 is the most recent
// snapshot (undo the last turn), 2 the one before it, and so on. ok is false
// when stepsBack is out of range.
func (l *Log) At(stepsBack int) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if stepsBack < 1 || stepsBack > len(l.entries) {
		return Entry{}, false
	}
	return l.entries[len(l.entries)-stepsBack], true
}

// Truncate drops the snapshot stepsBack from the end and everything newer than
// it, so after reverting to that point the next /revert steps further back.
func (l *Log) Truncate(stepsBack int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if stepsBack < 1 || stepsBack > len(l.entries) {
		return
	}
	l.entries = l.entries[:len(l.entries)-stepsBack]
}

// Render lists the snapshots newest-first for /snapshots, numbered by how many
// steps back each is (the number to pass to /revert).
func (l *Log) Render() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return "No snapshots yet. One is taken before each turn; use /revert to undo."
	}
	var b strings.Builder
	b.WriteString("Snapshots (newest first) — /revert [n] restores file state from before that turn:")
	for i := len(l.entries) - 1; i >= 0; i-- {
		stepsBack := len(l.entries) - i
		fmt.Fprintf(&b, "\n  %d  before: %s", stepsBack, l.entries[i].Label)
	}
	return b.String()
}
