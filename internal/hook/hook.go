// Package hook runs user-configurable lifecycle hooks. A hook is a shell
// command bound to a lifecycle event name; the engine fires events as a
// conversation turn progresses. For the PreToolUse event a non-zero exit
// blocks the tool from running; all other events are observe-only.
//
// Name mapping: the TS user-configurable hooks (the HooksSchema command kind)
// become package hook, constructor hook.New, type hook.Runner.
package hook

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Lifecycle event names. These are the keys callers register hooks under and
// the values passed to Fire's event parameter.
const (
	// EventSessionStart fires once when a session begins. Observe-only.
	EventSessionStart = "SessionStart"
	// EventPreToolUse fires before a tool runs. A non-zero exit blocks the tool.
	EventPreToolUse = "PreToolUse"
	// EventPostToolUse fires after a tool runs. Observe-only.
	EventPostToolUse = "PostToolUse"
)

// defaultTimeout bounds each individual hook command.
const defaultTimeout = 30 * time.Second

// Spec describes a single hook: a shell command to run via "bash -c".
type Spec struct {
	// Command is the shell command to run.
	Command string `json:"command"`
}

// Runner fires lifecycle hooks registered per event name. The zero value is
// not usable; construct one with New. A nil *Runner is a valid no-op receiver.
type Runner struct {
	hooks   map[string][]Spec
	timeout time.Duration
}

// New returns a Runner that fires the given hooks, keyed by event name. A nil
// or empty map is fine; Fire then becomes a no-op.
func New(hooks map[string][]Spec) *Runner {
	return &Runner{hooks: hooks, timeout: defaultTimeout}
}

// Fire runs every hook registered under event, in order, exposing event,
// toolName, and input to each command via the environment variables
// KORAI_EVENT, KORAI_TOOL_NAME, and KORAI_TOOL_INPUT.
//
// For EventPreToolUse, if a command exits non-zero (or fails to run or times
// out), Fire returns block=true with reason set to the command's trimmed
// combined output (or a default message if empty), stopping at the first
// blocking hook. For every other event all commands run and their exit codes
// are ignored; Fire always returns block=false. A nil receiver, an unknown
// event, or an empty command is a no-op returning (false, "").
func (r *Runner) Fire(ctx context.Context, event, toolName string, input json.RawMessage) (block bool, reason string) {
	if r == nil {
		return false, ""
	}
	specs := r.hooks[event]
	if len(specs) == 0 {
		return false, ""
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	env := append(os.Environ(),
		"KORAI_EVENT="+event,
		"KORAI_TOOL_NAME="+toolName,
		"KORAI_TOOL_INPUT="+string(input),
	)

	for _, spec := range specs {
		if spec.Command == "" {
			continue
		}

		ctx2, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(ctx2, "bash", "-c", spec.Command)
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		cancel()

		if err == nil {
			continue
		}

		if event == EventPreToolUse {
			reason := strings.TrimSpace(string(output))
			if reason == "" {
				reason = "blocked by PreToolUse hook"
			}
			return true, reason
		}

		// Observe-only events ignore exit codes but still surface unexpected
		// failures to the operator via structured logging (never the screen).
		slog.Warn("lifecycle hook command failed",
			"event", event,
			"tool", toolName,
			"err", err)
	}

	return false, ""
}
