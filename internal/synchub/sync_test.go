package synchub_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/session"
	"github.com/Nevaero/korai-code-cli/internal/synchub"
)

// mockHub is an in-memory blind hub implementing the four sync endpoints. It
// verifies the bearer auth header and the content-addressing contract
// (block_hash = hex(sha256(rawCiphertext))).
type mockHub struct {
	t      *testing.T
	syncID string

	mu      sync.Mutex
	seq     int64
	blocks  map[string][]byte
	entries []synchub.ManifestEntry
	puts    int
}

func newMockHub(t *testing.T, syncID string) *mockHub {
	return &mockHub{t: t, syncID: syncID, blocks: make(map[string][]byte)}
}

func (h *mockHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer "+h.syncID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPut && r.URL.Path == "/v1/sync/blocks":
		h.putBlock(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/sync/manifest":
		h.manifest(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/sync/blocks/"):
		h.getBlock(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/sync/tombstone":
		h.tombstone(w, r)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *mockHub) putBlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID     string `json:"item_id"`
		BlockHash  string `json:"block_hash"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Ciphertext)
	if err != nil {
		http.Error(w, "bad ciphertext", http.StatusBadRequest)
		return
	}
	want := sha256.Sum256(raw)
	if body.BlockHash != hex.EncodeToString(want[:]) {
		h.t.Errorf("client sent wrong block_hash: %s", body.BlockHash)
		http.Error(w, "hash mismatch", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.puts++
	h.seq++
	h.blocks[body.BlockHash] = raw
	h.entries = append(h.entries, synchub.ManifestEntry{
		ItemID: body.ItemID, BlockHash: body.BlockHash, Seq: h.seq, ByteSize: int64(len(raw)),
	})
	writeJSON(w, map[string]int64{"seq": h.seq})
}

func (h *mockHub) manifest(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []synchub.ManifestEntry
	next := since
	for _, e := range h.entries {
		if e.Seq > since {
			out = append(out, e)
			if e.Seq > next {
				next = e.Seq
			}
		}
	}
	writeJSON(w, synchub.Manifest{Entries: out, Next: next})
}

func (h *mockHub) getBlock(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/v1/sync/blocks/")
	h.mu.Lock()
	data, ok := h.blocks[hash]
	h.mu.Unlock()
	if !ok {
		http.Error(w, "no block", http.StatusNotFound)
		return
	}
	_, _ = w.Write(data)
}

func (h *mockHub) tombstone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID string `json:"item_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.entries = append(h.entries, synchub.ManifestEntry{ItemID: body.ItemID, Seq: h.seq, Tombstone: true})
	writeJSON(w, map[string]int64{"seq": h.seq})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testKey() []byte {
	k, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	return k
}

func newSyncer(t *testing.T, url, syncID string, store session.Store) *synchub.Syncer {
	t.Helper()
	cfg := synchub.Config{
		Enabled:    true,
		URL:        url,
		SyncID:     syncID,
		Key:        testKey(),
		Interval:   time.Hour,
		CursorPath: filepath.Join(t.TempDir(), "cursor"),
	}
	s, err := synchub.New(cfg, store, discardLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("New returned nil for an enabled config")
	}
	return s
}

func sampleMessages() []apiclient.Message {
	return []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hello"}}},
		{Role: apiclient.RoleAssistant, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hi there"}}},
	}
}

// TestPushPullRoundTrip is the core flow: device A pushes a session; device B
// pulls it into an empty store and reconstructs the conversation.
func TestPushPullRoundTrip(t *testing.T) {
	ctx := context.Background()
	syncID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hub := newMockHub(t, syncID)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	storeA := session.NewFileStore(t.TempDir())
	if err := storeA.Save(session.Record{ID: "s1", CWD: "/w", Model: "m", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}
	syncerA := newSyncer(t, srv.URL, syncID, storeA)
	if err := syncerA.Sync(ctx); err != nil {
		t.Fatalf("A sync: %v", err)
	}
	if hub.puts != 1 {
		t.Errorf("expected 1 block PUT, got %d", hub.puts)
	}

	storeB := session.NewFileStore(t.TempDir())
	syncerB := newSyncer(t, srv.URL, syncID, storeB)
	if err := syncerB.Sync(ctx); err != nil {
		t.Fatalf("B sync: %v", err)
	}

	got, err := storeB.Load("s1")
	if err != nil {
		t.Fatalf("B Load: %v", err)
	}
	if diff := cmp.Diff(sampleMessages(), got.Messages); diff != "" {
		t.Errorf("pulled messages mismatch (-want +got):\n%s", diff)
	}
	if got.CWD != "/w" || got.Model != "m" {
		t.Errorf("pulled metadata mismatch: %+v", got)
	}
}

// TestCursorAdvanceAndDedup verifies the cursor persists so a second Sync makes
// no redundant work, and that an unchanged local session is not re-pushed.
func TestCursorAdvanceAndDedup(t *testing.T) {
	ctx := context.Background()
	syncID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hub := newMockHub(t, syncID)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	store := session.NewFileStore(t.TempDir())
	_ = store.Save(session.Record{ID: "s1", CWD: "/w", Messages: sampleMessages()})
	s := newSyncer(t, srv.URL, syncID, store)

	if err := s.Sync(ctx); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	puts1 := hub.puts
	if err := s.Sync(ctx); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if hub.puts != puts1 {
		t.Errorf("unchanged session re-pushed: puts %d -> %d", puts1, hub.puts)
	}
}

// TestTombstoneDeletes verifies a deleted session propagates as a tombstone and
// is removed on the peer.
func TestTombstoneDeletes(t *testing.T) {
	ctx := context.Background()
	syncID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hub := newMockHub(t, syncID)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	dirA := t.TempDir()
	storeA := session.NewFileStore(dirA)
	_ = storeA.Save(session.Record{ID: "s1", CWD: "/w", Messages: sampleMessages()})
	syncerA := newSyncer(t, srv.URL, syncID, storeA)
	if err := syncerA.Sync(ctx); err != nil {
		t.Fatalf("A sync 1: %v", err)
	}

	// B pulls the session first.
	storeB := session.NewFileStore(t.TempDir())
	syncerB := newSyncer(t, srv.URL, syncID, storeB)
	if err := syncerB.Sync(ctx); err != nil {
		t.Fatalf("B sync 1: %v", err)
	}
	if _, err := storeB.Load("s1"); err != nil {
		t.Fatalf("B should have s1: %v", err)
	}

	// A deletes it locally; its next sync tombstones.
	if err := storeA.Delete("s1"); err != nil {
		t.Fatal(err)
	}
	if err := syncerA.Sync(ctx); err != nil {
		t.Fatalf("A sync 2: %v", err)
	}

	// B pulls the tombstone and drops the session.
	if err := syncerB.Sync(ctx); err != nil {
		t.Fatalf("B sync 2: %v", err)
	}
	if _, err := storeB.Load("s1"); err == nil {
		t.Error("expected s1 to be deleted on B after tombstone")
	}
}

// TestMergeUnion verifies pulling a longer remote history unions it into a
// shorter local one (append-only merge).
func TestMergeUnion(t *testing.T) {
	ctx := context.Background()
	syncID := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hub := newMockHub(t, syncID)
	srv := httptest.NewServer(hub)
	defer srv.Close()

	full := sampleMessages()

	// A pushes the full two-message history.
	storeA := session.NewFileStore(t.TempDir())
	_ = storeA.Save(session.Record{ID: "s1", CWD: "/w", Messages: full})
	syncerA := newSyncer(t, srv.URL, syncID, storeA)
	if err := syncerA.Sync(ctx); err != nil {
		t.Fatalf("A sync: %v", err)
	}

	// B already has only the first message locally.
	storeB := session.NewFileStore(t.TempDir())
	_ = storeB.Save(session.Record{ID: "s1", CWD: "/w", Messages: full[:1]})
	syncerB := newSyncer(t, srv.URL, syncID, storeB)
	if err := syncerB.Sync(ctx); err != nil {
		t.Fatalf("B sync: %v", err)
	}

	got, err := storeB.Load("s1")
	if err != nil {
		t.Fatalf("B Load: %v", err)
	}
	if diff := cmp.Diff(full, got.Messages); diff != "" {
		t.Errorf("merged history mismatch (-want +got):\n%s", diff)
	}
}

// TestDisabledIsNoOp verifies a disabled config yields a nil Syncer whose
// methods are safe no-ops (zero network calls).
func TestDisabledIsNoOp(t *testing.T) {
	s, err := synchub.New(synchub.Config{Enabled: false}, nil, nil)
	if err != nil {
		t.Fatalf("New(disabled): %v", err)
	}
	if s != nil {
		t.Fatal("expected nil Syncer when disabled")
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Errorf("nil Sync should be a no-op, got %v", err)
	}
	// Run on a nil Syncer must return immediately.
	s.Run(context.Background())
}
