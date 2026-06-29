// Package repomap implements the RepoMap tool: a read-only tool that returns a
// ranked, budget-fitted outline of the repository's source symbols, so the
// model can grasp the codebase layout without opening every file.
package repomap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	repomapcore "github.com/Nevaero/korai-code-cli/internal/repomap"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the RepoMap tool.
type Input struct {
	// TokenBudget caps the approximate size of the returned map. Optional; a
	// sensible default is used when zero or negative.
	TokenBudget int `json:"token_budget,omitempty" jsonschema:"description=approximate token budget for the map; 0 uses the default (~1024)"`
	// Focus lists files the map should foreground (paths relative to the repo
	// root). Their neighborhood is boosted in the ranking. Optional.
	Focus []string `json:"focus,omitempty" jsonschema:"description=files to foreground in the map; their related files are boosted in the ranking"`
}

// Tool implements tool.Tool for generating a repository map.
type Tool struct{}

// New returns a new RepoMap tool.
func New() *Tool { return &Tool{} }

// Name returns "RepoMap".
func (t *Tool) Name() string { return "RepoMap" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Return a ranked outline of the repository: the most important source files (by reference graph / PageRank) " +
		"and the key symbols (functions, types, classes) each defines, fitted to a token budget. " +
		"Use it to orient in an unfamiliar codebase before reading individual files. Pass focus to foreground files you are working on."
}

// InputSchema returns the JSON schema for RepoMap's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — RepoMap only reads source files.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — building the map mutates nothing.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows RepoMap; it is read-only in every mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, _ perm.Mode) perm.Decision {
	return perm.DecisionAllow
}

// Execute builds the repository map rooted at the working directory.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("repomap: invalid input: %w", err)
	}

	root := deps.WorkDir
	if root == "" {
		root = "."
	}

	out, err := repomapcore.New(root).Build(ctx, repomapcore.Options{
		TokenBudget: in.TokenBudget,
		Mentioned:   in.Focus,
	})
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("cannot build repo map: %v", err), IsError: true}, nil
	}
	if out == "" {
		return tool.Result{Content: "no source files found to map"}, nil
	}
	return tool.Result{Content: out}, nil
}
