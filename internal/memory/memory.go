// Package memory implements a file-backed, size-capped persistent memory store.
//
// The store owns a single human-editable markdown file (conventionally
// .korai/MEMORY.md) holding two kinds of entries:
//
//   - Facts — "key: value" lines under a "## Facts" heading, optionally
//     annotated "[pinned]" and/or "[keywords: a, b]". Setting an existing key
//     supersedes its value. A fact is always injected into the model's context
//     unless it is keyword-gated (has keywords and is not pinned), in which
//     case it is injected only when a keyword matches the user's message.
//   - Notes — free-text lines under a "## Notes" heading, optionally annotated
//     "[pinned]", "[tags: a, b]" and "[uses: N]" (a store-maintained recall
//     counter). Pinned notes are always injected; other notes surface through
//     lexical recall against the latest user message (see recall.go).
//
// Backward compatibility: a legacy file with no section headings (the flat
// line-per-note format this package used before) parses every line as a pinned
// note, so legacy content keeps its always-injected behavior.
//
// The on-disk size is held at or below a byte cap by evicting whole entries in
// utility order — least-recalled, oldest unpinned notes first, pinned entries
// and facts last — rather than blindly dropping the oldest line.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultMaxBytes is the default byte cap for a Store created with NewStore.
const DefaultMaxBytes = 25_000

// Per-turn write caps: a single agent turn may record at most this many facts
// and notes. The caps keep a chatty model from flooding the store; the counter
// resets at the start of each top-level turn (Store.ResetTurn, wired to the
// engine's SessionStart hook).
const (
	// MaxFactWritesPerTurn caps fact writes (including supersessions) per turn.
	MaxFactWritesPerTurn = 3
	// MaxNoteWritesPerTurn caps note writes per turn.
	MaxNoteWritesPerTurn = 2
)

// ErrTurnCap is returned by SetFact / AddNote when the per-turn write cap for
// that entry kind has been reached. Callers surface it as a soft error so the
// model consolidates instead of retrying.
var ErrTurnCap = errors.New("memory: per-turn write cap reached")

// Store is a file-backed, size-capped persistent memory.
//
// A Store owns a single markdown file at path; all mutations are
// load-modify-write under a mutex, and the total on-disk size is held at or
// below maxBytes by evicting whole entries in utility order (see evict).
type Store struct {
	path     string
	maxBytes int

	mu         sync.Mutex
	factWrites int // fact writes since the last ResetTurn
	noteWrites int // note writes since the last ResetTurn
}

// NewStore returns a Store backed by the file at path using the default byte cap.
func NewStore(path string) *Store {
	return NewStoreWithCap(path, DefaultMaxBytes)
}

// NewStoreWithCap returns a Store backed by the file at path with an explicit
// byte cap. A non-positive maxBytes falls back to DefaultMaxBytes.
func NewStoreWithCap(path string, maxBytes int) *Store {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Store{path: path, maxBytes: maxBytes}
}

// Read returns the current raw contents of the memory file. A missing file
// yields the empty string and no error; any other read failure is wrapped with %w.
func (s *Store) Read() (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("memory: reading %s: %w", s.path, err)
	}
	return string(data), nil
}

// Load reads and parses the memory file into its structured form. A missing
// file yields an empty File and no error.
func (s *Store) Load() (File, error) {
	raw, err := s.Read()
	if err != nil {
		return File{}, err
	}
	return Parse(raw), nil
}

// ResetTurn resets the per-turn write counters. The session wires it to the
// engine's SessionStart hook so the caps apply per top-level turn (sub-agent
// writes within the turn share the same budget on purpose).
func (s *Store) ResetTurn() {
	s.mu.Lock()
	s.factWrites = 0
	s.noteWrites = 0
	s.mu.Unlock()
}

// SetFact records a fact, superseding any existing fact with the same key (the
// superseding entry keeps the original's position so the file stays stable).
// It enforces the per-turn fact cap (ErrTurnCap) and the byte cap (eviction).
func (s *Store) SetFact(f Fact) error {
	f.Key = sanitizeLine(f.Key)
	f.Value = sanitizeLine(f.Value)
	if f.Key == "" || f.Value == "" {
		return fmt.Errorf("memory: fact needs a non-empty key and value")
	}
	if strings.Contains(f.Key, ":") {
		return fmt.Errorf("memory: fact key %q must not contain ':'", f.Key)
	}
	f.Keywords = sanitizeList(f.Keywords)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.factWrites >= MaxFactWritesPerTurn {
		return fmt.Errorf("%w (%d facts per turn)", ErrTurnCap, MaxFactWritesPerTurn)
	}

	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	replaced := false
	for i := range file.Facts {
		if file.Facts[i].Key == f.Key {
			file.Facts[i] = f
			replaced = true
			break
		}
	}
	if !replaced {
		file.Facts = append(file.Facts, f)
	}
	if err := s.saveLocked(file); err != nil {
		return err
	}
	s.factWrites++
	return nil
}

// AddNote appends a note. It enforces the per-turn note cap (ErrTurnCap) and
// the byte cap (eviction). A note whose text duplicates an existing note is a
// no-op (still counted against the cap so a repeating model cannot spin).
func (s *Store) AddNote(n Note) error {
	n.Text = sanitizeLine(n.Text)
	if n.Text == "" {
		return fmt.Errorf("memory: empty note")
	}
	n.Tags = sanitizeList(n.Tags)
	n.Uses = 0

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noteWrites >= MaxNoteWritesPerTurn {
		return fmt.Errorf("%w (%d notes per turn)", ErrTurnCap, MaxNoteWritesPerTurn)
	}

	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	for _, existing := range file.Notes {
		if existing.Text == n.Text {
			s.noteWrites++
			return nil
		}
	}
	file.Notes = append(file.Notes, n)
	if err := s.saveLocked(file); err != nil {
		return err
	}
	s.noteWrites++
	return nil
}

// RecordUses increments the recall counter of every note whose text is in
// texts, persisting the updated counters. Recall counters feed utility-based
// eviction; a failure only loses bookkeeping, so callers may ignore the error.
func (s *Store) RecordUses(texts []string) error {
	if len(texts) == 0 {
		return nil
	}
	set := make(map[string]bool, len(texts))
	for _, t := range texts {
		set[t] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	changed := false
	for i := range file.Notes {
		if set[file.Notes[i].Text] {
			file.Notes[i].Uses++
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked(file)
}

// loadLocked is Load without taking the mutex (the caller holds it).
func (s *Store) loadLocked() (File, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("memory: reading %s: %w", s.path, err)
	}
	return Parse(string(data)), nil
}

// saveLocked serializes file (evicting down to the byte cap first) and writes
// it, creating the parent directory as needed. The caller holds the mutex.
func (s *Store) saveLocked(file File) error {
	file = s.evict(file)
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("memory: creating dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(s.path, []byte(file.Marshal()), 0o644); err != nil {
		return fmt.Errorf("memory: writing %s: %w", s.path, err)
	}
	return nil
}

// evict drops whole entries until the serialized file fits the byte cap,
// in ascending utility order:
//
//  1. unpinned notes, least-recalled first (ties: oldest first),
//  2. pinned notes, oldest first,
//  3. keyword-gated (unpinned) facts, oldest first,
//  4. pinned facts, oldest first.
//
// The last remaining entry is never evicted, mirroring the old line store's
// "never split the only line" behavior for a single oversize entry.
func (s *Store) evict(file File) File {
	for len(file.Marshal()) > s.maxBytes && len(file.Facts)+len(file.Notes) > 1 {
		if i, ok := lowestUtilityNote(file.Notes, false); ok {
			file.Notes = append(file.Notes[:i], file.Notes[i+1:]...)
			continue
		}
		if i, ok := lowestUtilityNote(file.Notes, true); ok {
			file.Notes = append(file.Notes[:i], file.Notes[i+1:]...)
			continue
		}
		if i, ok := firstFact(file.Facts, false); ok {
			file.Facts = append(file.Facts[:i], file.Facts[i+1:]...)
			continue
		}
		if i, ok := firstFact(file.Facts, true); ok {
			file.Facts = append(file.Facts[:i], file.Facts[i+1:]...)
			continue
		}
		break
	}
	return file
}

// lowestUtilityNote returns the index of the least-recalled, oldest note with
// the given pinned state, or ok=false when none exists.
func lowestUtilityNote(notes []Note, pinned bool) (int, bool) {
	best, found := -1, false
	for i, n := range notes {
		if n.Pinned != pinned {
			continue
		}
		if !found || n.Uses < notes[best].Uses {
			best, found = i, true
		}
	}
	return best, found
}

// firstFact returns the index of the oldest fact with the given pinned state,
// or ok=false when none exists. Keyword-gated facts count as unpinned.
func firstFact(facts []Fact, pinned bool) (int, bool) {
	for i, f := range facts {
		if f.Pinned == pinned {
			return i, true
		}
	}
	return -1, false
}

// sanitizeLine collapses a value onto a single trimmed line so the
// line-oriented file format cannot be broken by embedded newlines.
func sanitizeLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// sanitizeList sanitizes each element and drops empties, returning nil for an
// all-empty list so annotations serialize cleanly.
func sanitizeList(in []string) []string {
	var out []string
	for _, v := range in {
		if c := sanitizeLine(v); c != "" {
			out = append(out, c)
		}
	}
	return out
}
