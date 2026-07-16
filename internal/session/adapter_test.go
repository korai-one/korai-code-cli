package session_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	korai "github.com/korai-one/korai-sdk-go"
	sdksession "github.com/korai-one/korai-sdk-go/session"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/session"
)

// sampleMessages is a conversation exercising every apiclient content-block
// variant, with block order interleaved, so the round-trip test proves order and
// every field survive the map-up/map-down adapter.
func sampleMessages() []apiclient.Message {
	return []apiclient.Message{
		{
			Role: apiclient.RoleUser,
			Content: []apiclient.ContentBlock{
				apiclient.TextBlock{Text: "look at this"},
				apiclient.ImageBlock{Source: "data:image/png;base64,AAAA"},
			},
		},
		{
			Role: apiclient.RoleAssistant,
			Content: []apiclient.ContentBlock{
				apiclient.TextBlock{Text: "on it"},
				apiclient.ToolCallBlock{ID: "call_1", Name: "Bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
				apiclient.TextBlock{Text: "and then"},
			},
		},
		{
			Role: apiclient.RoleUser,
			Content: []apiclient.ContentBlock{
				apiclient.ToolResultBlock{ToolCallID: "call_1", Content: "file.txt", IsError: false},
			},
		},
		{
			Role: apiclient.RoleUser,
			Content: []apiclient.ContentBlock{
				apiclient.ToolResultBlock{ToolCallID: "call_2", Content: "boom", IsError: true},
			},
		},
	}
}

// TestAdapterRoundTrip asserts apiclient.Message -> korai.SessionMessage ->
// apiclient.Message is lossless for the CLI's full block set.
func TestAdapterRoundTrip(t *testing.T) {
	in := sampleMessages()
	canonical := session.ToCanonicalMessages(in)
	got := session.FromCanonicalMessages(canonical)
	if diff := cmp.Diff(in, got); diff != "" {
		t.Fatalf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestToCanonicalBlockKinds checks each apiclient block maps to the expected
// canonical variant, preserving order.
func TestToCanonicalBlockKinds(t *testing.T) {
	m := apiclient.Message{
		Role: apiclient.RoleAssistant,
		Content: []apiclient.ContentBlock{
			apiclient.TextBlock{Text: "t"},
			apiclient.ToolCallBlock{ID: "id", Name: "n", Input: json.RawMessage(`{"a":1}`)},
			apiclient.ToolResultBlock{ToolCallID: "id", Content: "r", IsError: true},
			apiclient.ImageBlock{Source: "s"},
		},
	}
	c := session.ToCanonicalMessage(m)
	if c.Role != "assistant" {
		t.Fatalf("role = %q, want assistant", c.Role)
	}
	want := []korai.Block{
		korai.TextBlock{Text: "t"},
		korai.ToolUseBlock{ID: "id", Name: "n", Input: json.RawMessage(`{"a":1}`)},
		korai.ToolResultBlock{ToolCallID: "id", Content: "r", IsError: true},
		korai.ImageBlock{Source: "s"},
	}
	if diff := cmp.Diff(want, c.Blocks); diff != "" {
		t.Fatalf("block mapping mismatch (-want +got):\n%s", diff)
	}
}

// TestStoreReloadIdentical proves a session saved through the canonical SDK store
// (the real persistence path) reloads with byte-identical messages after the
// adapter round-trips them.
func TestStoreReloadIdentical(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(t *testing.T) sdksession.Store
	}{
		{"file", func(t *testing.T) sdksession.Store {
			return sdksession.NewFileStore(t.TempDir())
		}},
		{"sqlite", func(t *testing.T) sdksession.Store {
			st, err := sdksession.NewSQLiteStore(context.Background(), filepath.Join(t.TempDir(), "sessions.db"))
			if err != nil {
				t.Fatalf("open sqlite store: %v", err)
			}
			// Close the DB handle before TempDir cleanup, or Windows refuses to
			// unlink the still-open file.
			t.Cleanup(func() { _ = st.Close() })
			return st
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.store(t)
			msgs := sampleMessages()
			id := session.NewID()
			sess := korai.Session{
				ID: id, Created: time.Now().UTC().Truncate(time.Second),
				Updated: time.Now().UTC().Truncate(time.Second),
				CWD:     "/work", Model: "auto", Tool: session.Tool,
				Messages: session.ToCanonicalMessages(msgs),
			}
			if err := store.Save(sess); err != nil {
				t.Fatalf("save: %v", err)
			}
			loaded, err := store.Load(id)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got := session.FromCanonicalMessages(loaded.Messages)
			if diff := cmp.Diff(msgs, got); diff != "" {
				t.Fatalf("reloaded messages differ (-want +got):\n%s", diff)
			}
			if loaded.Tool != session.Tool {
				t.Fatalf("tool = %q, want %q", loaded.Tool, session.Tool)
			}
		})
	}
}
