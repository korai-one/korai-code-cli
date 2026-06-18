// Package session persists conversations to disk so they can be resumed. It
// stores apiclient messages via a tagged DTO (the ContentBlock interface does
// not round-trip through plain JSON), keeping apiclient free of persistence
// concerns.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// Record is one saved conversation.
type Record struct {
	ID       string
	Created  time.Time
	Updated  time.Time
	CWD      string
	Model    string
	Messages []apiclient.Message
}

// NewID returns a sortable, unique session id (timestamp + random suffix).
func NewID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Store is a directory of session files (one JSON file per session).
type Store struct {
	dir string
}

// NewStore returns a store rooted at dir. Files are created lazily on Save.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Save writes r to disk, creating the directory if needed.
func (s *Store) Save(r Record) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating session dir: %w", err)
	}
	dto := toDTO(r)
	data, err := json.MarshalIndent(dto, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session: %w", err)
	}
	path := filepath.Join(s.dir, r.ID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing session %s: %w", path, err)
	}
	return nil
}

// Load reads the session with the given id.
func (s *Store) Load(id string) (Record, error) {
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading session %s: %w", id, err)
	}
	var dto recordDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return Record{}, fmt.Errorf("decoding session %s: %w", id, err)
	}
	return fromDTO(dto), nil
}

// List returns all saved sessions, most recently updated first. A missing
// directory yields an empty list (not an error).
func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	var records []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		r, err := s.Load(id)
		if err != nil {
			continue // skip unreadable/corrupt files rather than failing the list
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Updated.After(records[j].Updated) })
	return records, nil
}

// Latest returns the most recently updated session for cwd, if any.
func (s *Store) Latest(cwd string) (Record, bool, error) {
	records, err := s.List()
	if err != nil {
		return Record{}, false, err
	}
	for _, r := range records {
		if r.CWD == cwd {
			return r, true, nil
		}
	}
	return Record{}, false, nil
}
