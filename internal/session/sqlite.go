package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"

	_ "modernc.org/sqlite" // pure-Go SQLite driver registered as "sqlite"
)

// sqliteDriver is the database/sql driver name registered by modernc.org/sqlite.
const sqliteDriver = "sqlite"

// schema is the SQLite migration. One row per session holds the full serialized
// message list as a JSON blob (encoded via the dto.go DTOs and passed through
// the Codec), so apiclient types never leak into storage. created/updated are
// stored as RFC 3339 nanosecond strings to round-trip time.Time exactly.
const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id       TEXT PRIMARY KEY,
	created  TEXT NOT NULL,
	updated  TEXT NOT NULL,
	cwd      TEXT NOT NULL,
	model    TEXT NOT NULL,
	enc      TEXT NOT NULL,
	messages BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_cwd_updated ON sessions(cwd, updated);
`

// timeFormat is the textual encoding used for created/updated columns. RFC 3339
// with nanoseconds sorts lexically in time order and round-trips time.Time.
const timeFormat = time.RFC3339Nano

// SQLiteStore persists sessions to a single SQLite database file using the
// pure-Go modernc.org/sqlite driver via database/sql. It implements Store. Each
// session is one row whose messages column holds the serialized message list,
// passed through a Codec (the at-rest-encryption seam, plaintext by default).
type SQLiteStore struct {
	db    *sql.DB
	codec Codec
}

// NewSQLiteStore opens (creating if absent) the database at path and runs the
// schema migration. The parent directory is created 0700 and the database file
// is the user's private session data. The returned store uses the plaintext
// codec; call WithCodec to change it.
func NewSQLiteStore(ctx context.Context, path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("creating session dir: %w", err)
	}
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		return nil, fmt.Errorf("opening session db %s: %w", path, err)
	}
	// Keep the file private to the user, matching FileStore's 0600 sessions.
	if _, statErr := os.Stat(path); statErr == nil {
		if cerr := os.Chmod(path, filePerm); cerr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("securing session db %s: %w", path, cerr)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating session db %s: %w", path, err)
	}
	return &SQLiteStore{db: db, codec: PlainCodec{}}, nil
}

// WithCodec sets the codec used to encode the messages blob (the seam for
// at-rest encryption) and returns the store for chaining. The codec's Name is
// recorded per row so Load can select the matching codec.
func (s *SQLiteStore) WithCodec(c Codec) *SQLiteStore {
	if c != nil {
		s.codec = c
	}
	return s
}

// Close releases the underlying database handle.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// codecFor returns the codec named in a row. Plaintext rows need no codec;
// otherwise the store's configured codec must match (you cannot decode what you
// lack the key for). Mirrors FileStore.codecFor.
func (s *SQLiteStore) codecFor(name string) (Codec, error) {
	if name == "" || name == (PlainCodec{}).Name() {
		return PlainCodec{}, nil
	}
	if s.codec != nil && s.codec.Name() == name {
		return s.codec, nil
	}
	return nil, fmt.Errorf("no codec %q to decode session", name)
}

// encodeMessages serializes msgs through the DTO layer and the codec.
func (s *SQLiteStore) encodeMessages(msgs []apiclient.Message) ([]byte, error) {
	dtos := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		dtos = append(dtos, msgToDTO(m))
	}
	data, err := json.Marshal(dtos)
	if err != nil {
		return nil, fmt.Errorf("encoding session messages: %w", err)
	}
	enc, err := s.codec.Encode(data)
	if err != nil {
		return nil, fmt.Errorf("encoding session messages: %w", err)
	}
	return enc, nil
}

// decodeMessages reverses encodeMessages using the codec named in the row.
func (s *SQLiteStore) decodeMessages(enc string, stored []byte) ([]apiclient.Message, error) {
	codec, err := s.codecFor(enc)
	if err != nil {
		return nil, err
	}
	plain, err := codec.Decode(stored)
	if err != nil {
		return nil, fmt.Errorf("decoding session messages: %w", err)
	}
	var dtos []messageDTO
	if err := json.Unmarshal(plain, &dtos); err != nil {
		return nil, fmt.Errorf("decoding session messages: %w", err)
	}
	msgs := make([]apiclient.Message, 0, len(dtos))
	for _, d := range dtos {
		msgs = append(msgs, msgFromDTO(d))
	}
	return msgs, nil
}

// Save upserts the whole record. The messages blob and metadata are replaced on
// conflict, so it both creates and updates. Updated is taken from r.Updated.
func (s *SQLiteStore) Save(r Record) error {
	blob, err := s.encodeMessages(r.Messages)
	if err != nil {
		return err
	}
	const q = `
INSERT INTO sessions (id, created, updated, cwd, model, enc, messages)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	updated  = excluded.updated,
	cwd      = excluded.cwd,
	model    = excluded.model,
	enc      = excluded.enc,
	messages = excluded.messages`
	_, err = s.db.ExecContext(context.Background(), q,
		r.ID,
		r.Created.Format(timeFormat),
		r.Updated.Format(timeFormat),
		r.CWD, r.Model, s.codec.Name(), blob,
	)
	if err != nil {
		return fmt.Errorf("saving session %s: %w", r.ID, err)
	}
	return nil
}

// Load returns the session with the given id, or an error wrapping
// fs.ErrNotExist if no such session exists (mirroring FileStore, whose Load
// surfaces os.ErrNotExist for a missing file).
func (s *SQLiteStore) Load(id string) (Record, error) {
	const q = `SELECT id, created, updated, cwd, model, enc, messages FROM sessions WHERE id = ?`
	row := s.db.QueryRowContext(context.Background(), q, id)
	rec, err := scanRecord(row, s)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, fmt.Errorf("reading session %s: %w", id, fs.ErrNotExist)
		}
		return Record{}, fmt.Errorf("reading session %s: %w", id, err)
	}
	return rec, nil
}

// List returns all saved sessions, most recently updated first.
func (s *SQLiteStore) List() ([]Record, error) {
	const q = `SELECT id, created, updated, cwd, model, enc, messages FROM sessions ORDER BY updated DESC`
	rows, err := s.db.QueryContext(context.Background(), q)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []Record
	for rows.Next() {
		rec, err := scanRecord(rows, s)
		if err != nil {
			continue // skip unreadable rows rather than failing the list
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	return records, nil
}

// Delete removes the session row with the given id. A missing row is not an
// error (delete is idempotent), so a sync tombstone applies cleanly even when
// the session is absent locally. It is not part of the Store interface; callers
// that need it (the sync client applying tombstones) type-assert for it.
func (s *SQLiteStore) Delete(id string) error {
	const q = `DELETE FROM sessions WHERE id = ?`
	if _, err := s.db.ExecContext(context.Background(), q, id); err != nil {
		return fmt.Errorf("deleting session %s: %w", id, err)
	}
	return nil
}

// Latest returns the most recently updated session for cwd, if any.
func (s *SQLiteStore) Latest(cwd string) (Record, bool, error) {
	const q = `SELECT id, created, updated, cwd, model, enc, messages FROM sessions WHERE cwd = ? ORDER BY updated DESC LIMIT 1`
	row := s.db.QueryRowContext(context.Background(), q, cwd)
	rec, err := scanRecord(row, s)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, false, nil
		}
		return Record{}, false, fmt.Errorf("finding latest session for %s: %w", cwd, err)
	}
	return rec, true, nil
}

// scanner abstracts *sql.Row and *sql.Rows so scanRecord serves Load/List/Latest.
type scanner interface {
	Scan(dest ...any) error
}

// scanRecord reads one row into a Record, decoding the messages blob.
func scanRecord(sc scanner, s *SQLiteStore) (Record, error) {
	var (
		rec              Record
		created, updated string
		enc              string
		blob             []byte
	)
	if err := sc.Scan(&rec.ID, &created, &updated, &rec.CWD, &rec.Model, &enc, &blob); err != nil {
		return Record{}, err
	}
	c, err := time.Parse(timeFormat, created)
	if err != nil {
		return Record{}, fmt.Errorf("parsing created time: %w", err)
	}
	u, err := time.Parse(timeFormat, updated)
	if err != nil {
		return Record{}, fmt.Errorf("parsing updated time: %w", err)
	}
	rec.Created, rec.Updated = c, u
	msgs, err := s.decodeMessages(enc, blob)
	if err != nil {
		return Record{}, err
	}
	rec.Messages = msgs
	return rec, nil
}
