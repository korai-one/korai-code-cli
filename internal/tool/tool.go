// Package tool defines the frozen Tool interface and shared types.
// This package is the most-shared contract in the codebase; it is owned by the
// coordinator and must not be modified without coordinator sign-off.
package tool

import (
	"context"
	"encoding/json"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
)

// Tool is the contract every agent-invokable action implements.
// Fail-closed: ReadOnly and ConcurrencySafe default to false.
type Tool interface {
	// Name is the stable identifier the model calls (e.g. "ReadFile", "Bash").
	Name() string

	// Description returns the prompt text shown to the model.
	Description(ctx context.Context) string

	// InputSchema returns the JSON schema generated from the tool's input struct.
	InputSchema() *jsonschema.Schema

	// Execute runs the tool. It MUST honor ctx cancellation, MUST validate input
	// explicitly before acting, and MUST NOT print to the screen.
	Execute(ctx context.Context, raw json.RawMessage, deps Deps) (Result, error)

	// ReadOnly reports whether the tool mutates state. Defaults to false (fail-closed).
	// Drives parallel execution eligibility and permission UX.
	ReadOnly() bool

	// ConcurrencySafe reports whether this tool may run in parallel with others.
	// Defaults to false. Read-only tools are typically safe.
	ConcurrencySafe() bool

	// CheckPermission returns allow/ask/deny before Execute is called.
	CheckPermission(ctx context.Context, raw json.RawMessage, mode perm.Mode) perm.Decision
}

// LSPReporter surfaces language-server diagnostics for a file right after its
// content changed: it notifies the server, waits for it to settle, and returns
// the rendered diagnostics block (empty when there are none). internal/lsp.Manager
// implements it. It lives here as a small interface so the tool package stays
// decoupled from the lsp package.
type LSPReporter interface {
	ReportAfterChange(ctx context.Context, path, content string, timeout time.Duration) string
	// ReportAfterChanges is the multi-file form, for tools (apply_patch) that
	// write several files at once: notify all, wait once, report each.
	ReportAfterChanges(ctx context.Context, files map[string]string, timeout time.Duration) string
}

// Deps carries injected dependencies available to every tool at execution time.
type Deps struct {
	// WorkDir is the working directory used to resolve relative paths.
	WorkDir string
	// LSP, when non-nil, appends language-server diagnostics to a file-writing
	// tool's result so the model can self-correct. nil disables it; tools must
	// nil-check before calling.
	LSP LSPReporter
}

// Result is the structured output of a tool execution.
type Result struct {
	// Content is the text representation returned to the model.
	Content string
	// IsError indicates the tool encountered an error (content is the error message).
	IsError bool
}
