// Package write implements the Write tool: a mutating tool that writes content
// to a file, creating it (and any missing parent directories) or overwriting it.
//
// Conceptual mapping: the TS FileWriteTool becomes package write, constructor
// write.New, type write.Tool.
package write

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// lspDiagnosticsTimeout bounds how long a write waits for the language server to
// report diagnostics before returning without them.
const lspDiagnosticsTimeout = 5 * time.Second

// Input is the structured input for the Write tool.
type Input struct {
	// Path is the path to the file to write, relative to the working directory.
	Path string `json:"path" jsonschema:"required,description=Path to the file to write, relative to the working directory"`
	// Content is the full contents to write to the file.
	Content string `json:"content" jsonschema:"required,description=The full contents to write"`
}

// Tool implements tool.Tool for writing files.
type Tool struct{}

// New returns a new Write tool.
func New() *Tool { return &Tool{} }

// Name returns "Write".
func (t *Tool) Name() string { return "Write" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Write content to a file at the given path, creating it (and any missing parent directories) or overwriting it if it already exists."
}

// InputSchema returns the JSON schema for Write's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — Write mutates the filesystem.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — writes are not safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission decides whether a Write may proceed for the given mode.
// Bypass and accept-edits modes allow silently; plan mode denies (no writes
// are permitted while planning); the default mode asks the user.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	switch mode {
	case perm.ModeBypassPermissions:
		return perm.DecisionAllow
	case perm.ModeAcceptEdits:
		return perm.DecisionAllow
	case perm.ModePlan:
		return perm.DecisionDeny
	case perm.ModeDefault:
		return perm.DecisionAsk
	default:
		// Fail-closed: any unrecognized mode asks before mutating.
		return perm.DecisionAsk
	}
}

// Execute writes Input.Content to the file at Input.Path, creating parent
// directories as needed. Invalid JSON is a hard error; an empty path or a
// filesystem failure is a soft error reported via Result.IsError.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("write: invalid input: %w", err)
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

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot create parent directory for %s: %v", path, err),
			IsError: true,
		}, nil
	}

	data := []byte(in.Content)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot write %s: %v", path, err),
			IsError: true,
		}, nil
	}

	content := fmt.Sprintf("wrote %d bytes to %s", len(data), path)
	// Append any language-server diagnostics for the written file so the model
	// sees compile/type errors it introduced and can fix them this turn.
	if deps.LSP != nil {
		content += deps.LSP.ReportAfterChange(ctx, path, string(data), lspDiagnosticsTimeout)
	}
	return tool.Result{Content: content}, nil
}
