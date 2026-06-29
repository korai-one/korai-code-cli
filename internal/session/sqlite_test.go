package session_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/session"
)

// newSQLiteStore opens a SQLiteStore backed by a fresh temp-dir db file.
func newSQLiteStore(t *testing.T) *session.SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fixedTime is a deterministic timestamp so tests never depend on the wall clock.
func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
}

// TestSQLiteSaveLoadRoundTrip verifies Save then Load reconstructs a Record with
// multiple messages (and the ContentBlock variants) exactly.
func TestSQLiteSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteStore(t)

	rec := session.Record{
		ID:       "test-1",
		Created:  fixedTime(),
		Updated:  fixedTime().Add(time.Minute),
		CWD:      "/work",
		Model:    "claude-sonnet-4-6",
		Messages: sampleMessages(),
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load("test-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(rec.Messages, got.Messages); diff != "" {
		t.Errorf("messages round-trip mismatch (-want +got):\n%s", diff)
	}
	if got.ID != rec.ID || got.CWD != rec.CWD || got.Model != rec.Model {
		t.Errorf("metadata mismatch: %+v", got)
	}
	if !got.Created.Equal(rec.Created) || !got.Updated.Equal(rec.Updated) {
		t.Errorf("timestamps mismatch: created=%v updated=%v", got.Created, got.Updated)
	}
}

// TestSQLiteLoadMissing verifies Load of an unknown id reports a not-found error
// matching FileStore's fs.ErrNotExist signal.
func TestSQLiteLoadMissing(t *testing.T) {
	t.Parallel()
	store := newSQLiteStore(t)
	if _, err := store.Load("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load missing: err = %v, want fs.ErrNotExist", err)
	}
}

// TestSQLiteSaveUpdates verifies a second Save with the same id and more
// messages updates the existing session in place.
func TestSQLiteSaveUpdates(t *testing.T) {
	t.Parallel()
	store := newSQLiteStore(t)
	msgs := sampleMessages()

	rec := session.Record{ID: "s", CWD: "/w", Created: fixedTime(), Updated: fixedTime(), Messages: msgs[:1]}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	rec.Messages = msgs
	rec.Updated = fixedTime().Add(time.Hour)
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save grown: %v", err)
	}

	got, err := store.Load("s")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(msgs, got.Messages); diff != "" {
		t.Errorf("updated history mismatch (-want +got):\n%s", diff)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("update created a new row: got %d sessions, want 1", len(list))
	}
}

// TestSQLiteListAndLatest verifies List returns saved sessions newest-first and
// Latest selects the newest session for a cwd (and false when none).
func TestSQLiteListAndLatest(t *testing.T) {
	t.Parallel()
	store := newSQLiteStore(t)

	now := fixedTime()
	recs := []session.Record{
		{ID: "a", CWD: "/p", Created: now, Updated: now.Add(-2 * time.Hour), Messages: sampleMessages()},
		{ID: "b", CWD: "/p", Created: now, Updated: now.Add(-1 * time.Hour), Messages: sampleMessages()},
		{ID: "c", CWD: "/other", Created: now, Updated: now, Messages: sampleMessages()},
	}
	for _, r := range recs {
		if err := store.Save(r); err != nil {
			t.Fatalf("Save %s: %v", r.ID, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 || list[0].ID != "c" || list[2].ID != "a" {
		t.Errorf("List not sorted newest-first: %+v", list)
	}

	latest, ok, err := store.Latest("/p")
	if err != nil || !ok {
		t.Fatalf("Latest: ok=%v err=%v", ok, err)
	}
	if latest.ID != "b" {
		t.Errorf("Latest = %s, want b", latest.ID)
	}

	if _, ok, err := store.Latest("/missing"); err != nil || ok {
		t.Errorf("Latest on unknown cwd: ok=%v err=%v, want false/nil", ok, err)
	}
}

// TestSQLiteCodecRoundTrip verifies the messages blob passes through the codec
// (the at-rest-encryption seam): stored bytes are not plaintext, the codec name
// is recorded, and a store without the codec cannot decode the row.
func TestSQLiteCodecRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	store.WithCodec(xorCodec{key: 0x5a})

	rec := session.Record{ID: "enc", CWD: "/w", Model: "m", Created: fixedTime(), Updated: fixedTime(), Messages: sampleMessages()}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if strings.Contains(string(raw), "let me check") {
		t.Errorf("message content stored in plaintext in db file")
	}

	got, err := store.Load("enc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(rec.Messages, got.Messages); diff != "" {
		t.Errorf("codec round-trip mismatch (-want +got):\n%s", diff)
	}
	_ = store.Close()

	// A store without the codec cannot decode the encrypted row.
	plain, err := session.NewSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	if _, err := plain.Load("enc"); err == nil {
		t.Error("expected error loading encrypted session without codec")
	}
}

// TestStoreInterface asserts both backends satisfy the Store interface.
func TestStoreInterface(t *testing.T) {
	t.Parallel()
	var _ session.Store = session.NewFileStore(t.TempDir())
	var _ session.Store = newSQLiteStore(t)
}
