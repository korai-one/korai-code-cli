// Package references implements the lsp_references tool: an on-demand,
// read-only query that asks the language server for every reference to the
// symbol at a given position, so the model can understand impact before
// changing a symbol.
package references

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

// referencesTimeout bounds the language-server request.
const referencesTimeout = 5 * time.Second

// Referencer is the slice of the LSP manager this tool needs. It is satisfied
// by *lsp.Manager; defining it here keeps the tool decoupled from the lsp package.
type Referencer interface {
	Enabled() bool
	ReferencesText(ctx context.Context, path, content string, line, column int, includeDeclaration bool, timeout time.Duration) (string, error)
}

// Input is the structured input for the lsp_references tool.
type Input struct {
	// Path is the file containing the symbol, relative to the working directory.
	Path string `json:"path" jsonschema:"required,description=Path to the file containing the symbol"`
	// Line is the 1-based line of the symbol.
	Line int `json:"line" jsonschema:"required,description=1-based line number of the symbol"`
	// Column is the 1-based column of the symbol; defaults to 1 when omitted.
	Column int `json:"column,omitempty" jsonschema:"description=1-based column of the symbol (default 1)"`
	// IncludeDeclaration also returns the symbol's own declaration.
	IncludeDeclaration bool `json:"include_declaration,omitempty" jsonschema:"description=also include the declaration site"`
}

// Tool implements tool.Tool for on-demand symbol references.
type Tool struct {
	lsp Referencer
}

// New returns a new lsp_references tool backed by the given LSP manager.
func New(lsp Referencer) *Tool { return &Tool{lsp: lsp} }

// Name returns "lsp_references".
func (t *Tool) Name() string { return "lsp_references" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "List every reference to the symbol at a given file position (line/column) using the language server. " +
		"Use it to gauge the impact of changing or removing a symbol. Requires a language server for the file's type."
}

// InputSchema returns the JSON schema for the tool's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — references only read.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — querying references mutates nothing.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows lsp_references; it is read-only in every mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, _ perm.Mode) perm.Decision {
	return perm.DecisionAllow
}

// Execute reads the file and returns references to the symbol at the position.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("lsp_references: invalid input: %w", err)
	}
	if in.Path == "" {
		return tool.Result{Content: "path is required", IsError: true}, nil
	}
	if in.Line < 1 {
		return tool.Result{Content: "line must be a 1-based line number", IsError: true}, nil
	}
	if t.lsp == nil || !t.lsp.Enabled() {
		return tool.Result{Content: "language-server references are unavailable (LSP is disabled or no server is installed)"}, nil
	}

	column := in.Column
	if column < 1 {
		column = 1
	}

	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(deps.WorkDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("cannot read %s: %v", path, err), IsError: true}, nil
	}

	out, err := t.lsp.ReferencesText(ctx, path, string(data), in.Line, column, in.IncludeDeclaration, referencesTimeout)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("references lookup failed: %v", err), IsError: true}, nil
	}
	if out == "" {
		return tool.Result{Content: fmt.Sprintf("no references found for the symbol at %s:%d:%d", path, in.Line, column)}, nil
	}
	return tool.Result{Content: "<references>\n" + out + "\n</references>"}, nil
}
