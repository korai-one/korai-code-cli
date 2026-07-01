package session_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/session"
)

func sampleMessages() []apiclient.Message {
	return []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hello"}}},
		{Role: apiclient.RoleAssistant, Content: []apiclient.ContentBlock{
			apiclient.TextBlock{Text: "let me check"},
			apiclient.ToolCallBlock{ID: "c1", Name: "ReadFile", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{
			apiclient.ToolResultBlock{ToolCallID: "c1", Content: "data", IsError: false},
		}},
	}
}

// TestSaveLoadRoundTrip verifies the DTO conversion preserves messages,
// including the ContentBlock interface variants.
func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	store := session.NewFileStore(t.TempDir())

	rec := session.Record{
		ID:       "test-1",
		Created:  time.Now().Truncate(time.Second),
		Updated:  time.Now().Truncate(time.Second),
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
	if got.CWD != "/work" || got.Model != "claude-sonnet-4-6" {
		t.Errorf("metadata mismatch: %+v", got)
	}
}

// TestAppendIncremental verifies that saving a longer history extends the file
// in place and still loads as the full conversation.
func TestAppendIncremental(t *testing.T) {
	t.Parallel()
	store := session.NewFileStore(t.TempDir())
	msgs := sampleMessages()

	// First turn: one message.
	rec := session.Record{ID: "s", CWD: "/w", Messages: msgs[:1]}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	// Later turns: the full (superset) history.
	rec.Messages = msgs
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save grown: %v", err)
	}

	got, err := store.Load("s")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(msgs, got.Messages); diff != "" {
		t.Errorf("appended history mismatch (-want +got):\n%s", diff)
	}
}

// TestSaveRewritesOnShrink verifies that a shorter history (as after
// compaction replaces it) rewrites the file rather than appending.
func TestSaveRewritesOnShrink(t *testing.T) {
	t.Parallel()
	store := session.NewFileStore(t.TempDir())
	msgs := sampleMessages()

	rec := session.Record{ID: "s", CWD: "/w", Messages: msgs}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save full: %v", err)
	}
	// Compaction replaces history with a single summary message.
	summary := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "summary"}}},
	}
	rec.Messages = summary
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save compacted: %v", err)
	}

	got, err := store.Load("s")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(summary, got.Messages); diff != "" {
		t.Errorf("rewritten history mismatch (-want +got):\n%s", diff)
	}
}

// TestFilePermissions verifies session files and their directory are private.
func TestFilePermissions(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "sessions")
	store := session.NewFileStore(dir)
	if err := store.Save(session.Record{ID: "p", CWD: "/w", Messages: sampleMessages()}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("dir perm = %o, want 700", got)
	}
	fi, err := os.Stat(filepath.Join(dir, "p.jsonl"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file perm = %o, want 600", got)
	}
}

// xorCodec is a reversible test codec proving the encryption seam wires
// through Save/Load. It XORs each byte and base64-encodes (newline-free).
type xorCodec struct{ key byte }

func (xorCodec) Name() string { return "test-xor" }

func (c xorCodec) Encode(plaintext []byte) ([]byte, error) {
	x := make([]byte, len(plaintext))
	for i, b := range plaintext {
		x[i] = b ^ c.key
	}
	return []byte(base64.StdEncoding.EncodeToString(x)), nil
}

func (c xorCodec) Decode(stored []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(string(stored))
	if err != nil {
		return nil, err
	}
	for i := range raw {
		raw[i] ^= c.key
	}
	return raw, nil
}

// TestCodecRoundTrip verifies message lines pass through the codec on the way
// to and from disk, that the codec name is recorded in the header, and that the
// stored message bytes are not plaintext.
func TestCodecRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := session.NewFileStore(dir).WithCodec(xorCodec{key: 0x5a})

	rec := session.Record{ID: "enc", CWD: "/w", Model: "m", Messages: sampleMessages()}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "enc.jsonl"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !strings.Contains(string(raw), `"enc":"test-xor"`) {
		t.Errorf("header does not record codec name:\n%s", raw)
	}
	if strings.Contains(string(raw), "let me check") {
		t.Errorf("message content stored in plaintext:\n%s", raw)
	}

	got, err := store.Load("enc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(rec.Messages, got.Messages); diff != "" {
		t.Errorf("codec round-trip mismatch (-want +got):\n%s", diff)
	}

	// A store without the codec cannot decode an encrypted file.
	if _, err := session.NewFileStore(dir).Load("enc"); err == nil {
		t.Error("expected error loading encrypted session without codec")
	}
}

func TestListSortedAndLatest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := session.NewFileStore(dir)

	recs := []session.Record{
		{ID: "a", CWD: "/p", Messages: sampleMessages()},
		{ID: "b", CWD: "/p", Messages: sampleMessages()},
		{ID: "c", CWD: "/other", Messages: sampleMessages()},
	}
	for _, r := range recs {
		if err := store.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	// Updated is derived from file mtime; set deterministic times: c newest,
	// then b, then a.
	now := time.Now()
	mtimes := map[string]time.Time{
		"a": now.Add(-2 * time.Hour),
		"b": now.Add(-1 * time.Hour),
		"c": now,
	}
	for id, mt := range mtimes {
		if err := os.Chtimes(filepath.Join(dir, id+".jsonl"), mt, mt); err != nil {
			t.Fatal(err)
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
}

func TestListMissingDir(t *testing.T) {
	t.Parallel()
	store := session.NewFileStore(t.TempDir() + "/does-not-exist")
	list, err := store.List()
	if err != nil {
		t.Fatalf("List on missing dir should not error: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestNewIDUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := session.NewID()
		if seen[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = true
	}
}
