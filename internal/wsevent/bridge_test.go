package wsevent_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/proto"
	"github.com/Nevaero/korai-code-cli/internal/tool"
	"github.com/Nevaero/korai-code-cli/internal/wsevent"
)

// feed builds a closed channel carrying evts in order, as the engine would.
func feed(evts ...engine.Event) <-chan engine.Event {
	ch := make(chan engine.Event, len(evts))
	for _, e := range evts {
		ch <- e
	}
	close(ch)
	return ch
}

// TestBridgeMapsEvents checks each engine event maps to the right proto event
// and that DoneEvent's history is returned to the caller.
func TestBridgeMapsEvents(t *testing.T) {
	history := []apiclient.Message{
		{Role: apiclient.RoleUser, Content: []apiclient.ContentBlock{apiclient.TextBlock{Text: "hi"}}},
	}
	var got []proto.ServerEvent
	send := func(ev proto.ServerEvent) error {
		got = append(got, ev)
		return nil
	}

	out, err := wsevent.Bridge(feed(
		engine.TextEvent{Text: "thinking"},
		engine.CompactedEvent{Before: 5, After: 2},
		engine.DoneEvent{Messages: history},
	), send)
	if err != nil {
		t.Fatalf("Bridge error: %v", err)
	}
	if len(out) != 1 || out[0].Role != apiclient.RoleUser {
		t.Errorf("returned history = %v, want the DoneEvent messages", out)
	}

	want := []proto.ServerEvent{
		proto.Text("thinking"),
		proto.Compact(5, 2),
		proto.Done(),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestBridgeToolPairing checks a start/result pair share one synthesized id,
// while a result with no preceding start (a tool blocked before execution) gets
// its own fresh, non-empty id.
func TestBridgeToolPairing(t *testing.T) {
	var got []proto.ServerEvent
	send := func(ev proto.ServerEvent) error {
		got = append(got, ev)
		return nil
	}

	_, err := wsevent.Bridge(feed(
		engine.ToolStartEvent{Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
		engine.ToolResultEvent{Name: "bash", Result: tool.Result{Content: "file.txt"}},
		engine.ToolResultEvent{Name: "write", Result: tool.Result{Content: "denied", IsError: true}},
	), send)
	if err != nil {
		t.Fatalf("Bridge error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}

	start := got[0].(proto.ToolStartEvent)
	paired := got[1].(proto.ToolResultEvent)
	lone := got[2].(proto.ToolResultEvent)

	if start.ID == "" {
		t.Error("tool_start id is empty")
	}
	if paired.ID != start.ID {
		t.Errorf("paired result id = %q, want start id %q", paired.ID, start.ID)
	}
	if lone.ID == "" {
		t.Error("lone result id is empty")
	}
	if lone.ID == start.ID {
		t.Error("lone result reused the paired id; want a fresh id")
	}
	if !lone.IsError {
		t.Error("lone result should carry is_error=true")
	}
}

// TestBridgeError checks an engine error becomes a proto error event and that
// no history is returned (an error turn carries none).
func TestBridgeError(t *testing.T) {
	var got []proto.ServerEvent
	send := func(ev proto.ServerEvent) error {
		got = append(got, ev)
		return nil
	}

	out, err := wsevent.Bridge(feed(
		engine.ErrorEvent{Err: errors.New("model exploded")},
	), send)
	if err != nil {
		t.Fatalf("Bridge error: %v", err)
	}
	if out != nil {
		t.Errorf("history = %v, want nil on error turn", out)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if e, ok := got[0].(proto.ErrorEvent); !ok || e.Message != "model exploded" {
		t.Errorf("got[0] = %#v, want error event 'model exploded'", got[0])
	}
}

// TestBridgeSendErrorStops checks a send failure halts forwarding and surfaces
// the error.
func TestBridgeSendErrorStops(t *testing.T) {
	wantErr := errors.New("write failed")
	calls := 0
	send := func(proto.ServerEvent) error {
		calls++
		return wantErr
	}

	_, err := wsevent.Bridge(feed(
		engine.TextEvent{Text: "one"},
		engine.TextEvent{Text: "two"},
	), send)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("send called %d times, want 1 (stop after first failure)", calls)
	}
}
