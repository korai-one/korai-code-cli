// Package memory implements the Remember tool: a writing tool that saves a short
// note to persistent memory so it can be recalled in future sessions.
//
// Conceptual mapping: the reference CLI's persistent-memory write action becomes
// package memory exporting tool.Tool via memory.New. The note is stored in a
// caller-supplied internal/memory.Store; the tool never touches user files.
package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/memory"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the Remember tool.
type Input struct {
	// Note is the note to remember.
	Note string `json:"note" jsonschema:"required,description=The note to remember"`
}

// Tool implements tool.Tool for saving notes to persistent memory.
type Tool struct {
	store *memory.Store
}

// New returns a new Remember tool backed by the given persistent memory store.
func New(store *memory.Store) *Tool {
	return &Tool{store: store}
}

// Name returns "Remember".
func (t *Tool) Name() string { return "Remember" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Saves a short note to persistent memory so it can be recalled in future sessions."
}

// InputSchema returns the JSON schema for Remember's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — Remember writes to the persistent memory file.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — appends to the memory file are serialized.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission always allows Remember regardless of permission mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	// Remember only writes to the managed memory file, never to user files, so
	// it is safe to allow without prompting in every permission mode.
	_ = mode
	return perm.DecisionAllow
}

// Execute saves Input.Note to persistent memory. Invalid input JSON is a hard
// error; an empty note or a store write failure is a soft error returned as a
// Result with IsError set. It honors ctx cancellation and never prints.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, _ tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("memory: invalid input: %w", err)
	}
	if in.Note == "" {
		return tool.Result{Content: "note is required", IsError: true}, nil
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	if err := t.store.Append(in.Note); err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot remember note: %v", err),
			IsError: true,
		}, nil
	}
	return tool.Result{Content: "remembered"}, nil
}
