// Package bash implements the Bash tool: it runs a shell command via "bash -c"
// and returns the combined stdout and stderr to the model.
//
// Name mapping: the TS BashTool becomes package bash, constructor bash.New,
// type bash.Tool. The tool mutates state (it can run arbitrary commands), so it
// is fail-closed: ReadOnly and ConcurrencySafe are both false.
package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// defaultTimeout is applied when Input.TimeoutMS is zero.
const defaultTimeout = 120000 * time.Millisecond

// Input is the structured input for the Bash tool.
type Input struct {
	// Command is the shell command to run. Required.
	Command string `json:"command" jsonschema:"required,description=The shell command to run"`
	// TimeoutMS is the timeout in milliseconds. Optional; defaults to 120000.
	TimeoutMS int `json:"timeout_ms,omitempty" jsonschema:"description=timeout in milliseconds, default 120000"`
}

// Tool implements tool.Tool for running shell commands.
type Tool struct{}

// New returns a new Bash tool.
func New() *Tool { return &Tool{} }

// Name returns "Bash".
func (t *Tool) Name() string { return "Bash" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Run a shell command and return its combined stdout and stderr output."
}

// InputSchema returns the JSON schema for Bash's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — Bash can mutate state (fail-closed).
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — Bash must run serially (fail-closed).
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission gates Bash execution by permission mode:
//   - ModeBypassPermissions: allow without prompting.
//   - ModePlan: deny — no shell execution is permitted while planning.
//   - ModeDefault and ModeAcceptEdits: ask the user before running.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	switch mode {
	case perm.ModeBypassPermissions:
		return perm.DecisionAllow
	case perm.ModePlan:
		return perm.DecisionDeny
	case perm.ModeDefault, perm.ModeAcceptEdits:
		return perm.DecisionAsk
	default:
		// Fail-closed: unknown modes ask rather than silently proceed.
		return perm.DecisionAsk
	}
}

// Execute runs Input.Command via "bash -c" in deps.WorkDir and returns the
// combined stdout and stderr. A non-zero exit, a timeout, or context
// cancellation are reported as soft errors (Result.IsError) so the model can
// react; only malformed input is returned as a hard error.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("bash: invalid input: %w", err)
	}
	if in.Command == "" {
		return tool.Result{Content: "command is required", IsError: true}, nil
	}

	timeout := defaultTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}

	// Derive a child context so the command is bounded by both the caller's
	// cancellation and the per-command timeout.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", in.Command)
	cmd.Dir = deps.WorkDir

	// CombinedOutput captures stdout and stderr interleaved.
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Distinguish timeout from caller cancellation from a non-zero exit.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return tool.Result{
				Content: fmt.Sprintf("%scommand timed out after %s", string(output), timeout),
				IsError: true,
			}, nil
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return tool.Result{
				Content: fmt.Sprintf("%scommand canceled", string(output)),
				IsError: true,
			}, nil
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return tool.Result{
				Content: fmt.Sprintf("%s\ncommand exited with code %d", string(output), exitErr.ExitCode()),
				IsError: true,
			}, nil
		}
		// Failure to launch (e.g. bash not found) is still surfaced as a soft
		// error so the model sees the cause rather than aborting the turn.
		return tool.Result{
			Content: fmt.Sprintf("%sfailed to run command: %v", string(output), err),
			IsError: true,
		}, nil
	}

	return tool.Result{Content: string(output)}, nil
}
