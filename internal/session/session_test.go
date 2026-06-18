package session_test

import (
	"encoding/json"
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
	store := session.NewStore(t.TempDir())

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

func TestListSortedAndLatest(t *testing.T) {
	t.Parallel()
	store := session.NewStore(t.TempDir())

	older := session.Record{ID: "a", Updated: time.Now().Add(-time.Hour), CWD: "/p", Messages: sampleMessages()}
	newer := session.Record{ID: "b", Updated: time.Now(), CWD: "/p", Messages: sampleMessages()}
	other := session.Record{ID: "c", Updated: time.Now(), CWD: "/other", Messages: sampleMessages()}
	for _, r := range []session.Record{older, newer, other} {
		if err := store.Save(r); err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 || list[0].Updated.Before(list[1].Updated) {
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
	store := session.NewStore(t.TempDir() + "/does-not-exist")
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
