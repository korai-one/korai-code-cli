package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/localproto"
)

// fakeWorker scripts a worker's north endpoint over a net.Pipe. reply is invoked
// once the Open frame is received; it writes the response frames.
type fakeWorker struct {
	reply func(conn net.Conn, open localproto.OpenPayload)

	mu    sync.Mutex
	opens []localproto.OpenPayload
}

func (w *fakeWorker) dial(_ context.Context) (io.ReadWriteCloser, error) {
	client, server := net.Pipe()
	go w.loop(server)
	return client, nil
}

func (w *fakeWorker) loop(conn net.Conn) {
	defer conn.Close()
	type frame struct {
		ft   localproto.FrameType
		body []byte
	}
	inbound := make(chan frame, 8)
	go func() {
		r := bufio.NewReader(conn)
		for {
			ft, body, err := localproto.ReadFrame(r)
			if err != nil {
				close(inbound)
				return
			}
			inbound <- frame{ft, body}
		}
	}()

	first, ok := <-inbound
	if !ok || first.ft != localproto.FrameHello {
		return
	}
	_ = localproto.WriteJSON(conn, localproto.FrameReady, localproto.ReadyPayload{Version: localproto.ProtocolVersion})

	for f := range inbound {
		if f.ft == localproto.FrameOpen {
			var op localproto.OpenPayload
			_ = localproto.Decode(f.body, &op)
			w.mu.Lock()
			w.opens = append(w.opens, op)
			w.mu.Unlock()
			w.reply(conn, op)
		}
	}
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var got []Event
	for e := range ch {
		got = append(got, e)
	}
	return got
}

func userMsg(text string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{TextBlock{Text: text}}}
}

func TestLocalWorkerStreamsTokensAndToolCall(t *testing.T) {
	worker := &fakeWorker{
		reply: func(conn net.Conn, _ localproto.OpenPayload) {
			_ = localproto.WriteFrame(conn, localproto.FrameToken, []byte("Hel"))
			_ = localproto.WriteFrame(conn, localproto.FrameToken, []byte("lo"))
			_ = localproto.WriteJSON(conn, localproto.FrameToolCall, localproto.ToolCallPayload{
				Name: "read", Args: json.RawMessage(`{"path":"a.go"}`),
			})
			_ = localproto.WriteJSON(conn, localproto.FrameUsage, localproto.UsagePayload{PromptTokens: 11, CompletionTokens: 2})
			_ = localproto.WriteJSON(conn, localproto.FrameDone, localproto.DonePayload{FinishReason: "stop"})
		},
	}
	c := newLocalWorkerClient("auto", "", worker.dial)

	ch, err := c.Complete(context.Background(), Request{Messages: []Message{userMsg("hi")}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	got := collect(t, ch)

	var text string
	var toolStart, toolComplete, done bool
	var usage Usage
	for _, e := range got {
		switch v := e.(type) {
		case TextDeltaEvent:
			text += v.Text
		case ToolCallStartEvent:
			if v.Name == "read" {
				toolStart = true
			}
		case ToolCallCompleteEvent:
			if v.Name == "read" && string(v.Input) == `{"path":"a.go"}` {
				toolComplete = true
			}
		case MessageCompleteEvent:
			done = true
			usage = v.Usage
		case ErrorEvent:
			t.Fatalf("unexpected error event: %v", v.Err)
		}
	}
	if text != "Hello" {
		t.Errorf("text = %q, want %q", text, "Hello")
	}
	if !toolStart || !toolComplete {
		t.Errorf("tool events: start=%v complete=%v, want both true", toolStart, toolComplete)
	}
	if !done || usage.InputTokens != 11 || usage.OutputTokens != 2 {
		t.Errorf("done=%v usage=%+v, want done with prompt=11 completion=2", done, usage)
	}
}

func TestLocalWorkerSendsTurnPayload(t *testing.T) {
	worker := &fakeWorker{
		reply: func(conn net.Conn, _ localproto.OpenPayload) {
			_ = localproto.WriteJSON(conn, localproto.FrameDone, localproto.DonePayload{FinishReason: "stop"})
		},
	}
	c := newLocalWorkerClient("auto", "", worker.dial)

	req := Request{
		Model:    "balanced",
		System:   "be terse",
		Tools:    []ToolDef{{Name: "read", Description: "read a file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{userMsg("hi")},
	}
	collect(t, mustComplete(t, c, req))

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.opens) != 1 {
		t.Fatalf("opens = %d, want 1", len(worker.opens))
	}
	op := worker.opens[0]
	if op.Model != "balanced" {
		t.Errorf("model = %q, want balanced (per-request override)", op.Model)
	}
	if op.System != "be terse" {
		t.Errorf("system = %q", op.System)
	}
	if len(op.Tools) != 1 || op.Tools[0].Name != "read" {
		t.Errorf("tools = %+v, want [read]", op.Tools)
	}
	if len(op.Messages) != 1 || op.Messages[0].Role != "user" || string(op.Messages[0].Content) != `"hi"` {
		t.Errorf("messages = %+v, want one user msg with content \"hi\"", op.Messages)
	}
}

// TestLocalWorkerSendsExtendedSampling verifies the extended sampling and
// constrained-decoding fields reach the Open frame with pointer semantics
// intact (a deliberate zero seed survives) and that ConstrainTools is
// forwarded for the worker to resolve.
func TestLocalWorkerSendsExtendedSampling(t *testing.T) {
	worker := &fakeWorker{
		reply: func(conn net.Conn, _ localproto.OpenPayload) {
			_ = localproto.WriteJSON(conn, localproto.FrameDone, localproto.DonePayload{FinishReason: "stop"})
		},
	}
	c := newLocalWorkerClient("auto", "", worker.dial)

	seed := 0
	topK := 40
	minP := 0.05
	temp := 0.2
	req := Request{
		Messages: []Message{userMsg("hi")},
		Sampling: Sampling{
			Temperature: &temp,
			Seed:        &seed,
			TopK:        &topK,
			MinP:        &minP,
		},
		Grammar:        "root ::= \"yes\"",
		ConstrainTools: true,
	}
	collect(t, mustComplete(t, c, req))

	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.opens) != 1 {
		t.Fatalf("opens = %d, want 1", len(worker.opens))
	}
	s := worker.opens[0].Sampling
	if s.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", s.Temperature)
	}
	if s.Seed == nil || *s.Seed != 0 {
		t.Errorf("seed = %v, want explicit 0 (deliberate zero must survive)", s.Seed)
	}
	if s.TopK == nil || *s.TopK != 40 {
		t.Errorf("top_k = %v, want 40", s.TopK)
	}
	if s.MinP == nil || *s.MinP != 0.05 {
		t.Errorf("min_p = %v, want 0.05", s.MinP)
	}
	if s.Grammar != "root ::= \"yes\"" {
		t.Errorf("grammar = %q", s.Grammar)
	}
	if !s.ConstrainTools {
		t.Error("constrain_tools not forwarded")
	}
	if s.RepeatPenalty != nil || s.FrequencyPenalty != nil || s.PresencePenalty != nil {
		t.Error("absent sampling fields must stay nil on the wire")
	}
}

func TestLocalWorkerErrorFrame(t *testing.T) {
	worker := &fakeWorker{
		reply: func(conn net.Conn, _ localproto.OpenPayload) {
			_ = localproto.WriteJSON(conn, localproto.FrameError, localproto.ErrorPayload{Message: "boom"})
		},
	}
	c := newLocalWorkerClient("auto", "", worker.dial)
	got := collect(t, mustComplete(t, c, Request{Messages: []Message{userMsg("hi")}}))
	if len(got) == 0 {
		t.Fatal("no events")
	}
	last, ok := got[len(got)-1].(ErrorEvent)
	if !ok || last.Err == nil {
		t.Fatalf("last event = %+v, want ErrorEvent", got[len(got)-1])
	}
}

func TestLocalWorkerImageMessageBecomesParts(t *testing.T) {
	msgs, err := convertToProtoMessages([]Message{{
		Role: RoleUser,
		Content: []ContentBlock{
			TextBlock{Text: "look"},
			ImageBlock{Source: "data:image/png;base64,AAAA"},
		},
	}})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// Content must be a parts array, not a plain string.
	var parts []map[string]any
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil {
		t.Fatalf("content is not a parts array: %v (%s)", err, msgs[0].Content)
	}
	if len(parts) != 2 || parts[0]["type"] != "text" || parts[1]["type"] != "image_url" {
		t.Errorf("parts = %+v, want [text, image_url]", parts)
	}
}

func mustComplete(t *testing.T, c Client, req Request) <-chan Event {
	t.Helper()
	ch, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return ch
}
