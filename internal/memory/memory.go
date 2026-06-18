// Package memory implements a file-backed, size-capped persistent memory store.
//
// Conceptual mapping: this is the Go equivalent of the reference CLI's persistent
// memory file (rooted at a single notes file with a byte cap). Notes are stored
// one per line; when the file exceeds the cap, whole oldest lines are dropped
// from the front so the store stays line-oriented and deterministic.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultMaxBytes is the default byte cap for a Store created with NewStore.
const DefaultMaxBytes = 25_000

// Store is a file-backed, size-capped persistent memory.
//
// A Store owns a single file at path; notes are appended as lines and the total
// on-disk size is held at or below maxBytes by evicting whole oldest lines.
type Store struct {
	path     string
	maxBytes int
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

// Read returns the current contents of the memory file. A missing file yields
// the empty string and no error; any other read failure is wrapped with %w.
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

// Append writes note as a single line to the memory file, creating the parent
// directory and file as needed. The note's trailing newlines are trimmed and a
// single newline is added. An empty or whitespace-only note is rejected with an
// error so no blank lines are ever written. After appending, the byte cap is
// enforced by dropping whole oldest lines from the front until the file is at or
// below the cap; a line is never split. Real I/O failures are wrapped with %w.
func (s *Store) Append(note string) error {
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf("memory: empty note")
	}

	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("memory: creating dir %s: %w", dir, err)
		}
	}

	existing, err := s.Read()
	if err != nil {
		return err
	}

	line := strings.TrimRight(note, "\n") + "\n"
	updated := existing + line
	updated = s.evict(updated)

	if err := os.WriteFile(s.path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("memory: writing %s: %w", s.path, err)
	}
	return nil
}

// evict drops whole oldest lines from the front of contents until its byte
// length is at or below the cap. It never splits a line: if a single trailing
// line alone exceeds the cap, that line is preserved intact.
func (s *Store) evict(contents string) string {
	for len(contents) > s.maxBytes {
		idx := strings.IndexByte(contents, '\n')
		if idx < 0 || idx+1 >= len(contents) {
			// Only one line remains; keep it whole rather than splitting.
			break
		}
		contents = contents[idx+1:]
	}
	return contents
}
