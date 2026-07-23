package agenteval

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
)

// Metrics is the trace-derived tally of one run. It is computed from the
// engine's event stream plus the recorded requests, so it distinguishes a run
// that got the right answer cleanly from one that churned (retries, vetoes,
// wrap-ups) on the way there.
type Metrics struct {
	// ModelCalls is how many requests the engine sent to the client.
	ModelCalls int `json:"model_calls"`
	// ToolCalls is how many tool executions actually started (ToolStartEvent).
	ToolCalls int `json:"tool_calls"`
	// ToolResults is every tool outcome surfaced, including blocked calls.
	ToolResults int `json:"tool_results"`
	// ToolErrors is how many of those outcomes were errors (tool failures,
	// denials, vetoes, truncation refusals).
	ToolErrors int `json:"tool_errors"`
	// Denials counts calls the permission layer refused.
	Denials int `json:"denials"`
	// LoopWarnings counts loop-detector corrective notices (second identical
	// no-progress call).
	LoopWarnings int `json:"loop_warnings"`
	// Vetoes counts calls the loop detector skipped outright (third repeat).
	Vetoes int `json:"vetoes"`
	// Truncations counts tool calls refused because the model's turn was cut
	// off at the output token limit.
	Truncations int `json:"truncations"`
	// FenceRetries counts malformed-fence retry turns (requests the engine
	// sent with ConstrainTools set — exactly the retry turns).
	FenceRetries int `json:"fence_retries"`
	// WrapUps counts forced graceful wrap-up turns (budget or loop exhaustion).
	WrapUps int `json:"wrap_ups"`
	// Compactions counts CompactedEvents (pre-turn or intra-turn).
	Compactions int `json:"compactions"`
}

// Marker substrings identifying engine outcomes in tool results and requests.
// They mirror the engine's user-facing notice texts; the engine deliberately
// reuses its existing event shapes for these outcomes (no dedicated event
// types), so text markers are the only trace signal. The offline scenarios in
// this package pin them — if the engine rewords a notice, those tests fail and
// the marker is updated alongside.
const (
	markerVeto       = "tool call skipped:"
	markerLoopWarn   = "[loop detector]"
	markerDenied     = "was not permitted"
	markerTruncation = "tool call not executed: response truncated"
	markerWrapUp     = "Tools are disabled for your next reply"
)

// collector folds an engine event stream (and afterwards the recorded
// requests) into a Result.
type collector struct {
	workDir    string
	metrics    Metrics
	seq        []string
	transcript strings.Builder
	finalText  strings.Builder
	history    []apiclient.Message
	err        error
}

func newCollector(workDir string) *collector {
	return &collector{workDir: workDir}
}

// observe tallies one engine event and appends its transcript line.
func (c *collector) observe(evt engine.Event) {
	switch v := evt.(type) {
	case engine.TextEvent:
		c.finalText.WriteString(v.Text)
		fmt.Fprintf(&c.transcript, "TEXT %q\n", c.normalize(v.Text))
	case engine.ToolStartEvent:
		c.metrics.ToolCalls++
		c.seq = append(c.seq, v.Name)
		fmt.Fprintf(&c.transcript, "TOOL_START %s %s\n", v.Name, v.Input)
	case engine.ToolResultEvent:
		c.metrics.ToolResults++
		if v.Result.IsError {
			c.metrics.ToolErrors++
		}
		content := v.Result.Content
		switch {
		case strings.HasPrefix(content, markerVeto):
			c.metrics.Vetoes++
		case strings.HasPrefix(content, markerTruncation):
			c.metrics.Truncations++
		case strings.Contains(content, markerDenied):
			c.metrics.Denials++
		}
		if strings.Contains(content, markerLoopWarn) {
			c.metrics.LoopWarnings++
		}
		fmt.Fprintf(&c.transcript, "TOOL_RESULT %s error=%v %q\n", v.Name, v.Result.IsError, c.normalize(content))
	case engine.CompactedEvent:
		c.metrics.Compactions++
		fmt.Fprintf(&c.transcript, "COMPACTED before=%d after=%d\n", v.Before, v.After)
	case engine.DoneEvent:
		c.history = v.Messages
		c.transcript.WriteString("DONE\n")
	case engine.ErrorEvent:
		c.err = v.Err
		fmt.Fprintf(&c.transcript, "ERROR %q\n", c.normalize(v.Err.Error()))
	}
}

// finish folds in the recorded requests (request-side metrics) and returns the
// assembled Result.
func (c *collector) finish(reqs []apiclient.Request) Result {
	c.metrics.ModelCalls = len(reqs)
	for _, req := range reqs {
		if req.ConstrainTools {
			c.metrics.FenceRetries++
		}
		if len(req.Tools) == 0 && strings.Contains(lastUserText(req.Messages), markerWrapUp) {
			c.metrics.WrapUps++
		}
	}
	return Result{
		Metrics:      c.metrics,
		ToolSequence: c.seq,
		Transcript:   c.transcript.String(),
		FinalText:    c.finalText.String(),
		History:      c.history,
		Requests:     reqs,
		Err:          c.err,
	}
}

// normalize rewrites absolute workdir paths (both separator styles) to
// "<workdir>" so transcripts are stable across runs and platforms.
func (c *collector) normalize(s string) string {
	if c.workDir == "" {
		return s
	}
	fwd := filepath.ToSlash(c.workDir)
	s = strings.ReplaceAll(s, c.workDir+string(filepath.Separator), "<workdir>/")
	s = strings.ReplaceAll(s, fwd+"/", "<workdir>/")
	s = strings.ReplaceAll(s, c.workDir, "<workdir>")
	return strings.ReplaceAll(s, fwd, "<workdir>")
}

// lastUserText returns the concatenated text blocks of the last user message.
func lastUserText(messages []apiclient.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != apiclient.RoleUser {
			continue
		}
		var b strings.Builder
		for _, blk := range messages[i].Content {
			if txt, ok := blk.(apiclient.TextBlock); ok {
				b.WriteString(txt.Text)
			}
		}
		return b.String()
	}
	return ""
}
