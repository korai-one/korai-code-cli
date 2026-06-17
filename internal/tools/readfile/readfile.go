// Package readfile implements the ReadFile tool: a read-only tool that reads
// the contents of a file and returns them to the model.
package readfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the ReadFile tool.
type Input struct {
	// Path is the path to the file to read, relative to the working directory.
	Path string `json:"path" jsonschema:"required,description=Path to the file to read"`
}

// Tool implements tool.Tool for reading files.
type Tool struct{}

// New returns a new ReadFile tool.
func New() *Tool { return &Tool{} }

// Name returns "ReadFile".
func (t *Tool) Name() string { return "ReadFile" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Read the contents of a file at the given path. Returns the file contents as text."
}

// InputSchema returns the JSON schema for ReadFile's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — ReadFile never mutates state.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — reading files is safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows ReadFile regardless of permission mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	// Reading a file is always allowed in every permission mode.
	_ = mode
	return perm.DecisionAllow
}

// Execute reads the file at Input.Path and returns its contents.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("readfile: invalid input: %w", err)
	}
	if in.Path == "" {
		return tool.Result{Content: "path is required", IsError: true}, nil
	}

	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(deps.WorkDir, path)
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot read %s: %v", path, err),
			IsError: true,
		}, nil
	}
	return tool.Result{Content: string(data)}, nil
}
