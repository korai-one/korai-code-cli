package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	korai "github.com/korai-one/korai-sdk-go"
	sdksession "github.com/korai-one/korai-sdk-go/session"
	"github.com/korai-one/korai-sdk-go/session/synchub"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// saveWithTimes writes a session to store and pins its file's mtime to when,
// so List's most-recently-updated-first ordering is deterministic in tests
// (FileStore.Load derives Updated from the file's mtime, not a stored field).
func saveWithTimes(t *testing.T, dir string, store sdksession.Store, id, cwd string, when time.Time, userText string) {
	t.Helper()
	if err := store.Save(korai.Session{
		ID: id, Created: when, CWD: cwd, Model: "auto",
		Messages: []korai.SessionMessage{
			{Role: string(apiclient.RoleUser), Blocks: []korai.Block{korai.TextBlock{Text: userText}}},
		},
	}); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", id, err)
	}
}

// TestListSessionSummariesOrdering checks the "sessions" event lists
// most-recently-updated first, matching store.List's contract.
func TestListSessionSummariesOrdering(t *testing.T) {
	dir := t.TempDir()
	store := sdksession.NewFileStore(dir)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	saveWithTimes(t, dir, store, "old", "/work", base, "oldest")
	saveWithTimes(t, dir, store, "mid", "/work", base.Add(time.Hour), "middle")
	saveWithTimes(t, dir, store, "new", "/work", base.Add(2*time.Hour), "newest")

	got, err := listSessionSummaries(store, "", 0)
	if err != nil {
		t.Fatalf("listSessionSummaries: %v", err)
	}
	wantOrder := []string{"new", "mid", "old"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d sessions, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d: got %q, want %q", i, got[i].ID, id)
		}
	}
}

// TestListSessionSummariesCwdFilter checks cwd narrows the list to sessions
// from that directory only, leaving the others out entirely.
func TestListSessionSummariesCwdFilter(t *testing.T) {
	dir := t.TempDir()
	store := sdksession.NewFileStore(dir)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	saveWithTimes(t, dir, store, "a", "/project-a", base, "in a")
	saveWithTimes(t, dir, store, "b", "/project-b", base.Add(time.Minute), "in b")

	got, err := listSessionSummaries(store, "/project-a", 0)
	if err != nil {
		t.Fatalf("listSessionSummaries: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %+v, want just session a", got)
	}
}

// TestListSessionSummariesLimitClamp checks the default and the max clamp: a
// zero Limit returns sessionsDefaultLimit results, and a Limit far past
// sessionsMaxLimit is capped at it rather than returning everything.
func TestListSessionSummariesLimitClamp(t *testing.T) {
	dir := t.TempDir()
	store := sdksession.NewFileStore(dir)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const total = sessionsMaxLimit + 10
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("s%04d", i)
		saveWithTimes(t, dir, store, id, "/work", base.Add(time.Duration(i)*time.Second), "msg")
	}

	def, err := listSessionSummaries(store, "", 0)
	if err != nil {
		t.Fatalf("listSessionSummaries (default): %v", err)
	}
	if len(def) != sessionsDefaultLimit {
		t.Errorf("default limit: got %d sessions, want %d", len(def), sessionsDefaultLimit)
	}

	capped, err := listSessionSummaries(store, "", total*10)
	if err != nil {
		t.Fatalf("listSessionSummaries (over max): %v", err)
	}
	if len(capped) != sessionsMaxLimit {
		t.Errorf("over-max limit: got %d sessions, want %d (clamped)", len(capped), sessionsMaxLimit)
	}

	small, err := listSessionSummaries(store, "", 3)
	if err != nil {
		t.Fatalf("listSessionSummaries (small): %v", err)
	}
	if len(small) != 3 {
		t.Errorf("small limit: got %d sessions, want 3", len(small))
	}
}

// TestSessionTitle checks the derived title collapses internal whitespace
// (including newlines) to a single line and truncates long prompts.
func TestSessionTitle(t *testing.T) {
	msgs := []korai.SessionMessage{
		{Role: string(apiclient.RoleUser), Blocks: []korai.Block{
			korai.TextBlock{Text: "  fix the   bug\nin the parser  "},
		}},
	}
	got := sessionTitle(msgs)
	if got != "fix the bug in the parser" {
		t.Errorf("got %q", got)
	}

	long := strings.Repeat("a", sessionTitleMaxLen+20)
	msgs = []korai.SessionMessage{
		{Role: string(apiclient.RoleUser), Blocks: []korai.Block{korai.TextBlock{Text: long}}},
	}
	got = sessionTitle(msgs)
	if len(got) != sessionTitleMaxLen+len("…") {
		t.Errorf("long title not trimmed to %d runes: got %q (len %d)", sessionTitleMaxLen, got, len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long title should end with an ellipsis: %q", got)
	}
}

// TestResumeSessionEmptyID checks an all-whitespace id is rejected before ever
// touching the store.
func TestResumeSessionEmptyID(t *testing.T) {
	called := false
	srv := &server{sess: &assembled{resumeLoad: func(string) ([]apiclient.Message, time.Time, error) {
		called = true
		return nil, time.Time{}, nil
	}}}
	if _, _, _, err := srv.resumeSession("   "); err == nil {
		t.Fatal("want an error for an empty id")
	}
	if called {
		t.Error("resumeLoad should not be called for an empty id")
	}
}

// TestResumeSessionUnknownID checks a load failure surfaces as a plain error
// naming the id, rather than the /resume slash command's silent bind-to-empty
// behavior.
func TestResumeSessionUnknownID(t *testing.T) {
	srv := &server{sess: &assembled{resumeLoad: func(id string) ([]apiclient.Message, time.Time, error) {
		return nil, time.Time{}, os.ErrNotExist
	}}}
	_, _, _, err := srv.resumeSession("ghost")
	if err == nil {
		t.Fatal("want an error for an unknown id")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the id: %v", err)
	}
}

// TestResumeSessionPopulatesHistory checks a successful resume returns the
// trimmed id and the store's history/created time unchanged — the exact values
// the worker loop assigns into its running history, sessionID and
// sessionStart.
func TestResumeSessionPopulatesHistory(t *testing.T) {
	want := []apiclient.Message{
		userMessage("hi"),
		{Role: apiclient.RoleAssistant, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hello"}}},
	}
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	srv := &server{sess: &assembled{resumeLoad: func(id string) ([]apiclient.Message, time.Time, error) {
		if id != "sess-1" {
			t.Errorf("resumeLoad called with %q, want sess-1", id)
		}
		return want, created, nil
	}}}

	id, history, gotCreated, err := srv.resumeSession(" sess-1 ")
	if err != nil {
		t.Fatalf("resumeSession: %v", err)
	}
	if id != "sess-1" {
		t.Errorf("id = %q, want sess-1", id)
	}
	if !gotCreated.Equal(created) {
		t.Errorf("created = %v, want %v", gotCreated, created)
	}
	if len(history) != len(want) {
		t.Fatalf("history len = %d, want %d", len(history), len(want))
	}
	for i := range want {
		if history[i].Role != want[i].Role {
			t.Errorf("history[%d].Role = %q, want %q", i, history[i].Role, want[i].Role)
		}
	}
}

// TestSyncFlushCloserSwallowsFailure checks the shutdown flush closer never
// surfaces a Sync failure (an unreachable hub here) as its own error — a flush
// failure must never change the exit path — and that it returns well within
// finalSyncTimeout rather than hanging.
func TestSyncFlushCloserSwallowsFailure(t *testing.T) {
	cfg := synchub.Config{
		Enabled:    true,
		URL:        "http://127.0.0.1:1", // nothing listens here: connection refused, fast
		SyncID:     "test-namespace",
		Key:        make([]byte, 32),
		Interval:   time.Minute,
		CursorPath: filepath.Join(t.TempDir(), "cursor"),
	}
	syncer, err := synchub.New(cfg, sdksession.NewFileStore(t.TempDir()), nil)
	if err != nil {
		t.Fatalf("synchub.New: %v", err)
	}

	closer := syncFlushCloser(syncer, cfg.SyncID)
	done := make(chan error, 1)
	go func() { done <- closer() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("closer() = %v, want nil (a flush failure must not propagate)", err)
		}
	case <-time.After(finalSyncTimeout + 2*time.Second):
		t.Fatal("closer did not return within finalSyncTimeout")
	}
}

// TestMarshalCanonicalMessages checks a resumed history renders in the
// canonical, block-tagged shape the ResumedEvent carries — role plus a "kind"-
// tagged block list — the same shape sessions are stored in.
func TestMarshalCanonicalMessages(t *testing.T) {
	raw, err := marshalCanonicalMessages([]apiclient.Message{userMessage("hi there")})
	if err != nil {
		t.Fatalf("marshalCanonicalMessages: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("got %d messages, want 1", len(raw))
	}
	var decoded struct {
		Role   string `json:"role"`
		Blocks []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(raw[0], &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Role != "user" {
		t.Errorf("role = %q, want user", decoded.Role)
	}
	if len(decoded.Blocks) != 1 || decoded.Blocks[0].Kind != "text" || decoded.Blocks[0].Text != "hi there" {
		t.Errorf("blocks = %+v", decoded.Blocks)
	}
}
