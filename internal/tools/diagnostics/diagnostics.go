// Package diagnostics implements the lsp_diagnostics tool: an on-demand,
// read-only query of the language server's diagnostics for a single file. It
// lets the model ask "what's wrong with this file?" without making an edit
// first (Edit/Write already append diagnostics after a change).
package diagnostics

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

// diagnosticsTimeout bounds how long the tool waits for the server to settle.
const diagnosticsTimeout = 5 * time.Second

// Diagnoser is the slice of the LSP manager this tool needs. It is satisfied by
// *lsp.Manager; defining it here keeps the tool decoupled from the lsp package.
type Diagnoser interface {
	Enabled() bool
	DiagnoseFile(ctx context.Context, path, content string, timeout time.Duration) string
}

// Input is the structured input for the lsp_diagnostics tool.
type Input struct {
	// Path is the file to diagnose, relative to the working directory.
	Path string `json:"path" jsonschema:"required,description=Path to the file to get language-server diagnostics for"`
}

// Tool implements tool.Tool for on-demand language-server diagnostics.
type Tool struct {
	lsp Diagnoser
}

// New returns a new lsp_diagnostics tool backed by the given LSP manager.
func New(lsp Diagnoser) *Tool { return &Tool{lsp: lsp} }

// Name returns "lsp_diagnostics".
func (t *Tool) Name() string { return "lsp_diagnostics" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Report language-server diagnostics (errors, warnings) for a file without editing it. " +
		"Useful to check a file's health or confirm a fix. Requires a language server for the file's type to be installed."
}

// InputSchema returns the JSON schema for the tool's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — diagnostics only read.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — querying diagnostics mutates nothing.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows lsp_diagnostics; it is read-only in every mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, _ perm.Mode) perm.Decision {
	return perm.DecisionAllow
}

// Execute reads the file and returns its language-server diagnostics.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("lsp_diagnostics: invalid input: %w", err)
	}
	if in.Path == "" {
		return tool.Result{Content: "path is required", IsError: true}, nil
	}
	if t.lsp == nil || !t.lsp.Enabled() {
		return tool.Result{Content: "language-server diagnostics are unavailable (LSP is disabled or no server is installed)"}, nil
	}

	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(deps.WorkDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("cannot read %s: %v", path, err), IsError: true}, nil
	}

	report := t.lsp.DiagnoseFile(ctx, path, string(data), diagnosticsTimeout)
	if report == "" {
		return tool.Result{Content: fmt.Sprintf("no diagnostics for %s", path)}, nil
	}
	return tool.Result{Content: report}, nil
}
