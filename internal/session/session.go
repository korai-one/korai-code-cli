// Package session persists conversations to disk so they can be resumed. Each
// session is a JSONL file: a header line of metadata followed by one line per
// conversation message, appended as the conversation grows (the whole file is
// rewritten only when history is replaced, e.g. by compaction). Messages are
// stored via a tagged DTO (the apiclient.ContentBlock interface does not
// round-trip through plain JSON), keeping apiclient free of persistence
// concerns. Message lines pass through a Codec so files can be encrypted at
// rest without changing callers; today the codec is the plaintext pass-through.
package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// fileExt is the on-disk extension for a session file.
const fileExt = ".jsonl"

// Permissions: session files hold conversation content and are kept private to
// the user, matching the directory.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Record is one saved conversation. Updated is derived from the file's
// modification time on Load/List, not stored in the file.
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

// Store is a directory of session files (one JSONL file per session).
type Store struct {
	dir   string
	codec Codec
}

// NewStore returns a store rooted at dir, using the plaintext codec. Files are
// created lazily on Save.
func NewStore(dir string) *Store { return &Store{dir: dir, codec: PlainCodec{}} }

// WithCodec sets the codec used to encode message lines (the seam for at-rest
// encryption) and returns the store for chaining. The codec's Name is recorded
// in each file's header so Load can select the matching codec.
func (s *Store) WithCodec(c Codec) *Store {
	if c != nil {
		s.codec = c
	}
	return s
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+fileExt) }

// codecFor returns the codec named in a file header. Plaintext files need no
// codec; otherwise the store's configured codec must match (you cannot decode
// what you lack the key for). Future codecs are selected here by name.
func (s *Store) codecFor(name string) (Codec, error) {
	if name == "" || name == (PlainCodec{}).Name() {
		return PlainCodec{}, nil
	}
	if s.codec != nil && s.codec.Name() == name {
		return s.codec, nil
	}
	return nil, fmt.Errorf("no codec %q to decode session", name)
}

// Save persists r. In the common case it appends only the messages not yet on
// disk; if the file is missing, or history has shrunk relative to disk (as
// after compaction replaces it), the whole file is rewritten from the header.
// Korai only ever extends or wholesale-replaces history, so a same-or-longer
// length is treated as an append.
func (s *Store) Save(r Record) error {
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("creating session dir: %w", err)
	}
	path := s.path(r.ID)
	onDisk, err := s.countMessages(path)
	if err != nil {
		return err
	}
	if onDisk < 0 || onDisk > len(r.Messages) {
		return s.rewrite(path, r)
	}
	return s.appendMessages(path, r.Messages[onDisk:])
}

// countMessages returns the number of message entries already in the file
// (total lines minus the header line), or -1 if the file does not exist. It
// counts newline bytes so arbitrarily long lines are handled.
func (s *Store) countMessages(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return 0, fmt.Errorf("opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	lines := 0
	buf := make([]byte, 32*1024)
	for {
		n, rerr := f.Read(buf)
		lines += bytes.Count(buf[:n], []byte{'\n'})
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, fmt.Errorf("scanning session %s: %w", path, rerr)
		}
	}
	if lines == 0 { // empty or header-only-without-newline: nothing appendable yet
		return 0, nil
	}
	return lines - 1, nil // exclude the header line
}

// rewrite truncates the file and writes the header followed by every message.
func (s *Store) rewrite(path string, r Record) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("creating session %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing session %s: %w", path, cerr)
		}
	}()
	// Enforce private perms even if the file pre-existed with looser ones.
	if cerr := f.Chmod(filePerm); cerr != nil {
		return fmt.Errorf("securing session %s: %w", path, cerr)
	}

	w := bufio.NewWriter(f)
	if err := s.writeHeader(w, r); err != nil {
		return err
	}
	for _, m := range r.Messages {
		if err := s.writeMessage(w, m); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing session %s: %w", path, err)
	}
	return nil
}

// appendMessages appends msgs to an existing session file. A no-op for none.
func (s *Store) appendMessages(path string, msgs []apiclient.Message) (err error) {
	if len(msgs) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("opening session %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing session %s: %w", path, cerr)
		}
	}()

	w := bufio.NewWriter(f)
	for _, m := range msgs {
		if err := s.writeMessage(w, m); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("appending session %s: %w", path, err)
	}
	return nil
}

// writeHeader writes the plaintext header line.
func (s *Store) writeHeader(w *bufio.Writer, r Record) error {
	data, err := json.Marshal(headerFromRecord(r, s.codec.Name()))
	if err != nil {
		return fmt.Errorf("encoding session header: %w", err)
	}
	return writeLine(w, data)
}

// writeMessage encodes m through the codec and writes it as one line.
func (s *Store) writeMessage(w *bufio.Writer, m apiclient.Message) error {
	data, err := json.Marshal(msgToDTO(m))
	if err != nil {
		return fmt.Errorf("encoding session message: %w", err)
	}
	enc, err := s.codec.Encode(data)
	if err != nil {
		return fmt.Errorf("encoding session message: %w", err)
	}
	return writeLine(w, enc)
}

func writeLine(w *bufio.Writer, data []byte) error {
	if _, err := w.Write(data); err != nil {
		return err
	}
	return w.WriteByte('\n')
}

// Load reads the session with the given id. Updated is set from the file's
// modification time.
func (s *Store) Load(id string) (Record, error) {
	path := s.path(id)
	f, err := os.Open(path)
	if err != nil {
		return Record{}, fmt.Errorf("reading session %s: %w", id, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Record{}, fmt.Errorf("stat session %s: %w", id, err)
	}

	r := bufio.NewReader(f)
	header, err := readHeader(r)
	if err != nil {
		return Record{}, fmt.Errorf("session %s: %w", id, err)
	}
	codec, err := s.codecFor(header.Enc)
	if err != nil {
		return Record{}, fmt.Errorf("session %s: %w", id, err)
	}

	rec := Record{
		ID: header.ID, Created: header.Created, Updated: info.ModTime(),
		CWD: header.CWD, Model: header.Model,
	}
	for {
		line, rerr := r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\n")
		if len(line) > 0 {
			m, derr := decodeMessage(codec, line)
			if derr != nil {
				return Record{}, fmt.Errorf("session %s: %w", id, derr)
			}
			rec.Messages = append(rec.Messages, m)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return Record{}, fmt.Errorf("reading session %s: %w", id, rerr)
		}
	}
	return rec, nil
}

// readHeader reads and validates the first line as a session header.
func readHeader(r *bufio.Reader) (headerDTO, error) {
	line, err := r.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return headerDTO{}, fmt.Errorf("reading header: %w", err)
	}
	line = bytes.TrimRight(line, "\n")
	if len(line) == 0 {
		return headerDTO{}, errors.New("empty session file")
	}
	var h headerDTO
	if err := json.Unmarshal(line, &h); err != nil {
		return headerDTO{}, fmt.Errorf("decoding header: %w", err)
	}
	if h.Kind != kindHeader {
		return headerDTO{}, fmt.Errorf("first line is not a header (kind %q)", h.Kind)
	}
	return h, nil
}

// decodeMessage decodes one stored message line through the codec.
func decodeMessage(codec Codec, line []byte) (apiclient.Message, error) {
	plain, err := codec.Decode(line)
	if err != nil {
		return apiclient.Message{}, fmt.Errorf("decoding message: %w", err)
	}
	var peek kindPeek
	if err := json.Unmarshal(plain, &peek); err != nil {
		return apiclient.Message{}, fmt.Errorf("decoding message: %w", err)
	}
	if peek.Kind != kindMessage {
		return apiclient.Message{}, fmt.Errorf("unexpected entry kind %q", peek.Kind)
	}
	var m messageDTO
	if err := json.Unmarshal(plain, &m); err != nil {
		return apiclient.Message{}, fmt.Errorf("decoding message: %w", err)
	}
	return msgFromDTO(m), nil
}

// List returns all saved sessions, most recently modified first. A missing
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), fileExt)
		r, err := s.Load(id)
		if err != nil {
			continue // skip unreadable/corrupt files rather than failing the list
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Updated.After(records[j].Updated) })
	return records, nil
}

// Latest returns the most recently modified session for cwd, if any.
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
