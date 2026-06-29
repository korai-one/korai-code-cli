// Package todo implements a session-scoped, concurrency-safe todo list used by
// the agent to track multi-step work within a single session.
//
// Conceptual mapping: this is the Go equivalent of the reference CLI's in-memory
// task list. It holds no persistence and touches no files — the list lives only
// for the duration of the session and is replaced wholesale on each update.
package todo

import (
	"strings"
	"sync"
)

// Status is a todo item's state.
type Status string

const (
	// StatusPending marks a task that has not been started.
	StatusPending Status = "pending"
	// StatusInProgress marks the task currently being worked on.
	StatusInProgress Status = "in_progress"
	// StatusCompleted marks a finished task.
	StatusCompleted Status = "completed"
)

// Item is one task. ActiveForm is the present-continuous label shown while the
// task is in progress (e.g. "Running tests" for content "Run tests").
type Item struct {
	Content    string
	Status     Status
	ActiveForm string
}

// List is a session-scoped, concurrency-safe todo list. The zero value is an
// empty, ready-to-use list.
type List struct {
	mu    sync.Mutex
	items []Item
}

// Set replaces the whole list with a copy of items. A nil or empty slice clears
// the list. The caller's slice is never retained.
func (l *List) Set(items []Item) {
	cp := make([]Item, len(items))
	copy(cp, items)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = cp
}

// Items returns a snapshot copy of the current items. Mutating the returned
// slice does not affect the list.
func (l *List) Items() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()

	cp := make([]Item, len(l.items))
	copy(cp, l.items)
	return cp
}

// Render returns the list as a markdown checklist, one item per line:
// "[ ]" for pending, "[~]" for in_progress, "[x]" for completed. The in_progress
// item is shown using its ActiveForm when one is set. When the list is empty a
// friendly placeholder line is returned instead.
func (l *List) Render() string {
	items := l.Items()
	if len(items) == 0 {
		return "(no todos yet)"
	}

	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(marker(it.Status))
		b.WriteByte(' ')
		b.WriteString(label(it))
	}
	return b.String()
}

// marker returns the checklist marker for a status.
func marker(s Status) string {
	switch s {
	case StatusInProgress:
		return "[~]"
	case StatusCompleted:
		return "[x]"
	default:
		return "[ ]"
	}
}

// label returns the text shown for an item: the ActiveForm while in progress
// (when set), otherwise the Content.
func label(it Item) string {
	if it.Status == StatusInProgress && it.ActiveForm != "" {
		return it.ActiveForm
	}
	return it.Content
}
