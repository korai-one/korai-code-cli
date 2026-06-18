package compact_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/compact"
)

// fakeClient returns a fixed summary, recording the request it received.
type fakeClient struct {
	summary string
	gotReq  apiclient.Request
}

func (c *fakeClient) Complete(_ context.Context, req apiclient.Request) (<-chan apiclient.Event, error) {
	c.gotReq = req
	ch := make(chan apiclient.Event, 2)
	ch <- apiclient.TextDeltaEvent{Text: c.summary}
	ch <- apiclient.MessageCompleteEvent{StopReason: "end_turn"}
	close(ch)
	return ch, nil
}

func userMsg(text string) apiclient.Message {
	return apiclient.Message{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: text}}}
}

func TestCompactReplacesOlderMessages(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "earlier: did X and Y"}
	msgs := []apiclient.Message{
		userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"), userMsg("5"), userMsg("6"),
	}

	out, err := compact.Compact(context.Background(), client, msgs, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// 1 summary message + 2 recent = 3.
	if len(out) != 3 {
		t.Fatalf("got %d messages, want 3", len(out))
	}
	summary := out[0].Content[0].(apiclient.TextBlock).Text
	if !strings.Contains(summary, "earlier: did X and Y") {
		t.Errorf("summary message = %q", summary)
	}
	// The last two original messages are preserved verbatim.
	if out[1].Content[0].(apiclient.TextBlock).Text != "5" {
		t.Errorf("recent[0] = %q, want 5", out[1].Content[0].(apiclient.TextBlock).Text)
	}
	if out[2].Content[0].(apiclient.TextBlock).Text != "6" {
		t.Errorf("recent[1] = %q, want 6", out[2].Content[0].(apiclient.TextBlock).Text)
	}
}

func TestCompactNoopWhenShort(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "unused"}
	msgs := []apiclient.Message{userMsg("1"), userMsg("2")}

	out, err := compact.Compact(context.Background(), client, msgs, 4)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("short history should be unchanged, got %d", len(out))
	}
	if client.gotReq.System != "" {
		t.Error("model should not be called for a short history")
	}
}

func TestCompactEmptySummaryErrors(t *testing.T) {
	t.Parallel()
	client := &fakeClient{summary: "   "}
	msgs := []apiclient.Message{userMsg("1"), userMsg("2"), userMsg("3"), userMsg("4"), userMsg("5")}

	if _, err := compact.Compact(context.Background(), client, msgs, 2); err == nil {
		t.Error("expected error on empty summary")
	}
}
