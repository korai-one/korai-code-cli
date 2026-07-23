package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/Nevaero/korai-code-cli/internal/localproto"
)

// LocalWorkerClient implements Client against a co-located Korai worker over the
// direct local channel (hop 1 of the local fast path): a persistent Unix-domain
// socket speaking the localproto binary framing, instead of the loopback
// OpenAI-HTTP path KoraiClient uses. It streams tokens as they decode and
// receives structured tool calls as frames, so the local path needs no fence
// round-trip and no buffered whole-reply parse.
//
// As with the other clients, no wire type crosses this boundary: localproto
// frames are converted to apiclient's own Event types at the edge.
//
// The connection is established lazily on the first Complete and reused across
// turns (keeping the worker's session — and its KV cache — warm). Turns are
// serialized: the CLI drives one turn at a time.
type LocalWorkerClient struct {
	model string
	token string
	dial  func(ctx context.Context) (io.ReadWriteCloser, error)

	// turnSem serializes Complete calls: one turn in flight at a time.
	turnSem chan struct{}

	// mu guards the connection for connect/drop; not held for a whole turn.
	mu     sync.Mutex
	conn   io.ReadWriteCloser
	reader *bufio.Reader

	// writeMu serializes frame writes (the turn trigger vs. an async cancel).
	writeMu sync.Mutex
}

// NewLocalWorkerClient creates a client that dials a co-located worker's
// Unix-domain socket at socketPath. model is the routing alias/id stamped on
// each request unless a per-request model overrides it.
func NewLocalWorkerClient(socketPath, model string) *LocalWorkerClient {
	return newLocalWorkerClient(model, "", dialer("unix", socketPath))
}

// NewLocalWorkerClientTCP creates a client that dials a worker over TCP at
// address — a dedicated home/LAN inference server. token, when non-empty, is
// presented in the handshake for the worker to validate.
func NewLocalWorkerClientTCP(address, token, model string) *LocalWorkerClient {
	return newLocalWorkerClient(model, token, dialer("tcp", address))
}

// dialer returns a dial func for the given network/address.
func dialer(network, address string) func(ctx context.Context) (io.ReadWriteCloser, error) {
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
}

// newLocalWorkerClient is the injectable constructor used by tests to supply a
// fake transport in place of a real dial.
func newLocalWorkerClient(model, token string, dial func(ctx context.Context) (io.ReadWriteCloser, error)) *LocalWorkerClient {
	return &LocalWorkerClient{
		model:   model,
		token:   token,
		dial:    dial,
		turnSem: make(chan struct{}, 1),
	}
}

// Complete implements Client. It sends the turn to the worker and streams the
// worker's frames back as Events.
func (c *LocalWorkerClient) Complete(ctx context.Context, req Request) (<-chan Event, error) {
	select {
	case c.turnSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if err := c.ensureConn(ctx); err != nil {
		<-c.turnSem
		return nil, fmt.Errorf("local worker connect: %w", err)
	}
	if err := c.sendTurn(req); err != nil {
		c.dropConn()
		<-c.turnSem
		return nil, fmt.Errorf("local worker send: %w", err)
	}

	ch := make(chan Event, 64)
	go func() {
		defer func() { <-c.turnSem }()
		c.pump(ctx, ch)
	}()
	return ch, nil
}

// ensureConn dials (once) and performs the Hello/Ready handshake.
func (c *LocalWorkerClient) ensureConn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return nil
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	if err := localproto.WriteJSON(conn, localproto.FrameHello, localproto.HelloPayload{Version: localproto.ProtocolVersion, Token: c.token}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("hello: %w", err)
	}
	ft, body, err := localproto.ReadFrame(reader)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read ready: %w", err)
	}
	if ft != localproto.FrameReady {
		_ = conn.Close()
		return fmt.Errorf("expected ready, got %s", ft)
	}
	var ready localproto.ReadyPayload
	if err := localproto.Decode(body, &ready); err != nil {
		_ = conn.Close()
		return err
	}
	if ready.Version != localproto.ProtocolVersion {
		_ = conn.Close()
		return fmt.Errorf("worker protocol version %d != %d", ready.Version, localproto.ProtocolVersion)
	}
	c.conn = conn
	c.reader = reader
	return nil
}

// sendTurn writes the Open frame carrying the full turn.
func (c *LocalWorkerClient) sendTurn(req Request) error {
	msgs, err := convertToProtoMessages(req.Messages)
	if err != nil {
		return err
	}
	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	open := localproto.OpenPayload{
		SessionID: uuid.NewString(),
		Model:     model,
		System:    req.System,
		Tools:     convertToProtoTools(req.Tools),
		Messages:  msgs,
		Sampling:  convertToProtoSampling(req),
	}
	return c.writeJSON(localproto.FrameOpen, open)
}

// convertToProtoSampling maps a request's sampling and constrained-decoding
// parameters onto the wire form. The pointer fields forward as-is (nil =
// absent, deliberate zero survives). Temperature/TopP ride non-pointer
// omitempty wire fields, so an explicit zero degrades to "worker default" —
// the same wire limitation as the worker repo's hostproto. ConstrainTools is
// forwarded rather than resolved here: the worker generates the fence grammar
// itself from the turn's ToolDefs (its generator is authoritative for its own
// engine), unless an explicit Grammar/JSONSchema is present, which wins.
func convertToProtoSampling(req Request) localproto.Sampling {
	s := localproto.Sampling{
		MaxTokens:        int(req.MaxTokens),
		Grammar:          req.Grammar,
		JSONSchema:       req.JSONSchema,
		Seed:             req.Sampling.Seed,
		TopK:             req.Sampling.TopK,
		MinP:             req.Sampling.MinP,
		RepeatPenalty:    req.Sampling.RepeatPenalty,
		FrequencyPenalty: req.Sampling.FrequencyPenalty,
		PresencePenalty:  req.Sampling.PresencePenalty,
		ConstrainTools:   req.ConstrainTools,
	}
	if req.Sampling.Temperature != nil {
		s.Temperature = *req.Sampling.Temperature
	}
	if req.Sampling.TopP != nil {
		s.TopP = *req.Sampling.TopP
	}
	return s
}

// pump reads frames until a terminal Done/Error (or a broken connection) and
// translates them into Events. It keeps reading to the terminal frame even after
// the consumer stops, so the reused connection stays byte-aligned.
func (c *LocalWorkerClient) pump(ctx context.Context, ch chan<- Event) {
	defer close(ch)

	watcherDone := make(chan struct{})
	defer close(watcherDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.writeFrame(localproto.FrameCancel, nil)
		case <-watcherDone:
		}
	}()

	var usage Usage
	sending := true
	deliver := func(e Event) bool {
		select {
		case ch <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for {
		ft, body, err := localproto.ReadFrame(c.reader)
		if err != nil {
			c.dropConn()
			if sending {
				deliver(ErrorEvent{Err: fmt.Errorf("local worker stream: %w", err)})
			}
			return
		}
		switch ft {
		case localproto.FrameToken:
			if sending {
				sending = deliver(TextDeltaEvent{Text: string(body)})
			}
		case localproto.FrameToolCall:
			var tc localproto.ToolCallPayload
			if err := localproto.Decode(body, &tc); err != nil {
				continue
			}
			id := tc.ID
			if id == "" {
				id = uuid.NewString()
			}
			input := tc.Args
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			if sending {
				sending = deliver(ToolCallStartEvent{ID: id, Name: tc.Name})
			}
			if sending {
				sending = deliver(ToolCallCompleteEvent{ID: id, Name: tc.Name, Input: input})
			}
		case localproto.FrameUsage:
			var u localproto.UsagePayload
			if err := localproto.Decode(body, &u); err == nil {
				usage = Usage{InputTokens: int64(u.PromptTokens), OutputTokens: int64(u.CompletionTokens)}
			}
		case localproto.FrameDone:
			var d localproto.DonePayload
			_ = localproto.Decode(body, &d)
			reason := NormalizeStopReason(d.FinishReason)
			if sending {
				deliver(MessageCompleteEvent{StopReason: reason, Usage: usage})
			}
			return
		case localproto.FrameError:
			var e localproto.ErrorPayload
			_ = localproto.Decode(body, &e)
			if sending {
				deliver(ErrorEvent{Err: errors.New(e.Message)})
			}
			return
		default:
			// Unknown frame type: ignore, keep the stream aligned.
		}
	}
}

// dropConn closes and forgets the connection so the next Complete redials.
func (c *LocalWorkerClient) dropConn() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.reader = nil
}

func (c *LocalWorkerClient) writeJSON(t localproto.FrameType, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(t, payload)
}

func (c *LocalWorkerClient) writeFrame(t localproto.FrameType, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("no worker connection")
	}
	return localproto.WriteFrame(conn, t, payload)
}

// --- conversion ----------------------------------------------------------

// convertToProtoTools maps apiclient ToolDefs to the wire form.
func convertToProtoTools(tools []ToolDef) []localproto.ToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]localproto.ToolDef, len(tools))
	for i, t := range tools {
		out[i] = localproto.ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	return out
}

// contentPart mirrors the OpenAI content-parts shape used for multimodal turns.
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

// convertToProtoMessages flattens block-structured messages into wire messages.
// Tool calls and tool results are rendered into the same fence dialect the
// worker already understands (reusing renderToolCallFence / renderToolResultText),
// while images become an OpenAI content-parts array so multimodal turns survive.
func convertToProtoMessages(msgs []Message) ([]localproto.Msg, error) {
	out := make([]localproto.Msg, 0, len(msgs))
	toolNames := make(map[string]string)

	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			var text strings.Builder
			var extra []string
			var images []string
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolResultBlock:
					extra = append(extra, renderToolResultText(toolNames[v.ToolCallID], v.Content, v.IsError))
				case ImageBlock:
					images = append(images, v.Source)
				default:
					return nil, fmt.Errorf("unsupported block %T in user message", b)
				}
			}
			joined := text.String()
			if len(extra) > 0 {
				if joined != "" {
					joined += "\n\n"
				}
				joined += strings.Join(extra, "\n\n")
			}
			content, err := encodeContent(joined, images)
			if err != nil {
				return nil, err
			}
			out = append(out, localproto.Msg{Role: "user", Content: content})

		case RoleAssistant:
			var text strings.Builder
			for _, b := range m.Content {
				switch v := b.(type) {
				case TextBlock:
					text.WriteString(v.Text)
				case ToolCallBlock:
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(renderToolCallFence(v.Name, v.Input))
					toolNames[v.ID] = v.Name
				default:
					return nil, fmt.Errorf("unsupported block %T in assistant message", b)
				}
			}
			content, err := json.Marshal(text.String())
			if err != nil {
				return nil, err
			}
			out = append(out, localproto.Msg{Role: "assistant", Content: content})

		default:
			return nil, fmt.Errorf("unknown role %q", m.Role)
		}
	}
	return out, nil
}

// encodeContent renders a message body as a plain JSON string when there are no
// images, or as an OpenAI content-parts array (text + image_url) when there are.
func encodeContent(text string, images []string) (json.RawMessage, error) {
	if len(images) == 0 {
		return json.Marshal(text)
	}
	parts := make([]contentPart, 0, len(images)+1)
	if text != "" {
		parts = append(parts, contentPart{Type: "text", Text: text})
	}
	for _, src := range images {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{URL: src}})
	}
	return json.Marshal(parts)
}
