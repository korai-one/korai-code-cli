// Package edit implements the Edit tool: string-replacement editing of an
// existing file. It replaces an exact old string with a new string, by default
// requiring the old string to occur exactly once.
//
// Conceptual mapping: the TS FileEditTool becomes package edit with constructor
// edit.New and type edit.Tool.
package edit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/editmatch"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// lspDiagnosticsTimeout bounds how long a write waits for the language server to
// report diagnostics before returning without them.
const lspDiagnosticsTimeout = 5 * time.Second

// Input is the structured input for the Edit tool.
type Input struct {
	// Path is the path to the file to edit, relative to the working directory.
	Path string `json:"path" jsonschema:"required,description=Path to the file to edit"`
	// OldString is the exact text to replace. By default it must appear exactly once.
	OldString string `json:"old_string" jsonschema:"required,description=exact text to replace"`
	// NewString is the replacement text.
	NewString string `json:"new_string" jsonschema:"required,description=replacement text"`
	// ReplaceAll, when true, replaces every occurrence instead of requiring uniqueness.
	ReplaceAll bool `json:"replace_all,omitempty" jsonschema:"description=replace every occurrence instead of requiring uniqueness"`
}

// Tool implements tool.Tool for string-replacement editing of an existing file.
type Tool struct{}

// New returns a new Edit tool.
func New() *Tool { return &Tool{} }

// Name returns "Edit".
func (t *Tool) Name() string { return "Edit" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Replace an exact string in a file. By default the old string must appear " +
		"exactly once; set replace_all to replace every occurrence."
}

// InputSchema returns the JSON schema for Edit's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — Edit mutates the target file.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — Edit writes files and must not run in parallel.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission returns allow/ask/deny for an Edit invocation based on mode.
// ModeBypassPermissions and ModeAcceptEdits allow silently, ModePlan denies
// (no writes permitted while planning), and ModeDefault asks.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	switch mode {
	case perm.ModeBypassPermissions:
		return perm.DecisionAllow
	case perm.ModeAcceptEdits:
		return perm.DecisionAllow
	case perm.ModePlan:
		return perm.DecisionDeny
	default:
		return perm.DecisionAsk
	}
}

// Execute replaces OldString with NewString in the file at Input.Path and
// writes the result back, preserving the file's existing permissions.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("edit: invalid input: %w", err)
	}
	if in.Path == "" {
		return tool.Result{Content: "path is required", IsError: true}, nil
	}
	if in.OldString == in.NewString {
		return tool.Result{Content: "old_string and new_string are identical", IsError: true}, nil
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
	content := string(data)

	// editmatch tries a cascade of matchers (exact → whitespace/indentation
	// tolerant → block-anchor → …) so the edit lands even when the model's
	// old_string drifts slightly from the file, and enforces uniqueness unless
	// replace_all is set.
	updated, replaced, err := editmatch.Replace(content, in.OldString, in.NewString, in.ReplaceAll)
	if err != nil {
		switch {
		case errors.Is(err, editmatch.ErrNotFound):
			return tool.Result{Content: fmt.Sprintf("old_string not found in %s", path), IsError: true}, nil
		case errors.Is(err, editmatch.ErrNotUnique):
			return tool.Result{
				Content: fmt.Sprintf("old_string is not unique in %s — provide more surrounding context or set replace_all", path),
				IsError: true,
			}, nil
		default:
			return tool.Result{Content: fmt.Sprintf("edit %s: %v", path, err), IsError: true}, nil
		}
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return tool.Result{
			Content: fmt.Sprintf("cannot write %s: %v", path, err),
			IsError: true,
		}, nil
	}

	out := fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, path)
	// Append any language-server diagnostics for the edited file so the model
	// sees compile/type errors it introduced and can fix them this turn.
	if deps.LSP != nil {
		out += deps.LSP.ReportAfterChange(ctx, path, updated, lspDiagnosticsTimeout)
	}
	return tool.Result{Content: out}, nil
}
