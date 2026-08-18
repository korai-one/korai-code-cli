// Package agenteval is the deterministic eval harness for the agent loop.
//
// It has two layers:
//
//   - Offline scenarios (Run): a scripted sequence of model turns is replayed
//     through a mock apiclient.Client against the REAL engine and REAL tools in
//     a temp working directory, then scored deterministically — expected tool
//     sequence, final file contents, a golden transcript, and metric assertions
//     tallied from the engine's event stream. This layer is plain `go test`
//     (see scenarios_test.go) so it is gated by the normal check pipeline.
//
//   - Live smoke (RunLive): a small set of real-model scenarios run headless
//     against a real endpoint, each in its own temp workdir with a per-run
//     timeout and a pinned sampling seed, scored by deterministic checks only
//     (file result, fence compliance, turns used) with JSONL records and a
//     summary table. Opt-in: the `korai eval` subcommand skips it entirely
//     when no endpoint is configured.
//
// The trace-derived Metrics struct is what separates "right answer" from
// "planner churn": a scenario can pass on the final file yet fail on the number
// of iterations, retries, or vetoes it burned getting there.
package agenteval

import (
	"github.com/Nevaero/korai-code-cli/internal/apiclient"
	"github.com/Nevaero/korai-code-cli/internal/engine"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Call is one scripted tool call within a Turn. Input is the literal JSON body
// as a string so a scenario can script a malformed body on purpose.
type Call struct {
	ID    string
	Name  string
	Input string
}

// Turn is one scripted model reply: optional prose, zero or more tool calls,
// and the stop reason the backend reports. An empty Stop defaults to
// apiclient.StopToolUse when the turn carries calls and apiclient.StopEndTurn
// otherwise, matching what a well-behaved backend would emit.
type Turn struct {
	Text  string
	Calls []Call
	Stop  string
}

// events renders the turn as the apiclient event sequence a backend would emit.
func (t Turn) events() []apiclient.Event {
	var evs []apiclient.Event
	if t.Text != "" {
		evs = append(evs, apiclient.TextDeltaEvent{Text: t.Text})
	}
	for _, c := range t.Calls {
		evs = append(evs,
			apiclient.ToolCallStartEvent{ID: c.ID, Name: c.Name},
			apiclient.ToolCallCompleteEvent{ID: c.ID, Name: c.Name, Input: []byte(c.Input)},
		)
	}
	stop := t.Stop
	if stop == "" {
		if len(t.Calls) > 0 {
			stop = apiclient.StopToolUse
		} else {
			stop = apiclient.StopEndTurn
		}
	}
	return append(evs, apiclient.MessageCompleteEvent{StopReason: stop})
}

// Scenario is one offline eval case: fixture files, a scripted model, and the
// engine configuration to run them under. Everything is deterministic — the
// only nondeterminism in a run would be a bug.
type Scenario struct {
	// Name identifies the scenario (also the golden transcript's file name).
	Name string
	// System is the base system prompt passed to the engine.
	System string
	// Prompt is the user prompt that starts the run.
	Prompt string
	// History is optional pre-run conversation history, prepended before the
	// prompt (e.g. to exercise compaction of older turns).
	History []apiclient.Message
	// Files maps workdir-relative paths to fixture contents written before the
	// run.
	Files map[string]string
	// Turns is the scripted model: reply i is returned for the engine's i-th
	// model call. A call beyond the script is a scenario failure (Result.Err).
	Turns []Turn
	// Mode is the permission mode the run executes under.
	Mode perm.Mode
	// Asker resolves "ask" decisions; nil defaults to perm.DenyAsker (the
	// headless default), which is how a denial scenario is scripted.
	Asker perm.Asker
	// Setup, called after the workdir is populated, returns the real tools to
	// register and any extra engine options (turn budget, auto-compaction,
	// memory section, …). It may capture the workdir to build stateful
	// dependencies such as a memory store.
	Setup func(workDir string) (tools []tool.Tool, opts []engine.Option)
}

// Result is what a scenario run produced, ready for deterministic scoring.
type Result struct {
	// Metrics is the trace-derived tally of the run.
	Metrics Metrics
	// ToolSequence is the names of the tools the engine actually executed
	// (ToolStartEvent order). Blocked or vetoed calls do not appear here —
	// they surface in Metrics and the transcript.
	ToolSequence []string
	// Transcript is the normalized event transcript for golden comparison.
	// Absolute workdir paths are rewritten to "<workdir>" so it is stable
	// across runs and platforms.
	Transcript string
	// FinalText is the concatenation of every TextEvent the engine emitted.
	FinalText string
	// History is the full post-run conversation from the DoneEvent.
	History []apiclient.Message
	// Requests are the requests the engine sent to the (scripted) client, for
	// asserting on what the model was shown (system sections, stripped tools,
	// ConstrainTools, sampling).
	Requests []apiclient.Request
	// Err is the engine's terminal error, if any (including script exhaustion).
	Err error
}
