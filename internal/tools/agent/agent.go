// Package agent implements the Task tool: a tool that delegates a focused
// sub-task to a sub-agent and returns the sub-agent's final text output.
//
// The actual sub-agent execution is provided via an injected Spawner interface
// so this package never imports the engine — avoiding an import cycle and
// keeping the tool unit-testable with a fake Spawner.
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Spawner runs a sub-agent to completion and returns its final text output.
// It is injected so the agent package does not depend on the engine.
type Spawner interface {
	// Spawn runs a sub-agent for the given prompt to completion, returning its
	// final text output or an error.
	Spawn(ctx context.Context, prompt string) (string, error)
}

// Input is the structured input for the Task tool.
type Input struct {
	// Description is a short description of the sub-task.
	Description string `json:"description" jsonschema:"required,description=A short description of the sub-task"`
	// Prompt is the full instruction for the sub-agent.
	Prompt string `json:"prompt" jsonschema:"required,description=The full instruction for the sub-agent"`
}

// Tool implements tool.Tool for delegating a sub-task to a sub-agent.
type Tool struct {
	spawner Spawner
}

// New returns a new Task tool that delegates sub-tasks to the given Spawner.
func New(spawner Spawner) *Tool {
	return &Tool{spawner: spawner}
}

// Name returns "Task".
func (t *Tool) Name() string { return "Task" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Run a sub-agent to handle a focused sub-task and return its final " +
		"result. The sub-agent has its own set of tools but cannot itself spawn " +
		"further Task sub-agents. Use this to delegate self-contained work that " +
		"benefits from a fresh, focused context."
}

// InputSchema returns the JSON schema for Task's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — a sub-agent may mutate state.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — a sub-agent may do anything, so spawning is
// not safe to parallelize (fail-closed).
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission allows Task only when permissions are bypassed; every other
// mode asks first, because spawning an autonomous agent is significant.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	if mode == perm.ModeBypassPermissions {
		return perm.DecisionAllow
	}
	return perm.DecisionAsk
}

// Execute spawns a sub-agent for Input.Prompt and returns its final output.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("agent: invalid input: %w", err)
	}
	if in.Prompt == "" {
		return tool.Result{Content: "prompt is required", IsError: true}, nil
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	out, err := t.spawner.Spawn(ctx, in.Prompt)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("sub-agent failed: %v", err),
			IsError: true,
		}, nil
	}
	return tool.Result{Content: out}, nil
}
