// Package applypatch implements the ApplyPatch tool: it applies a codex-style
// multi-file patch envelope (*** Begin Patch … *** End Patch) in one call,
// adding, updating (with fuzzy context matching), deleting, and moving files.
//
// The patch parsing/locating/application is pure (internal/patch); this tool is
// the thin I/O + permission + diagnostics shell around it: it reads the files
// the patch needs, applies in memory, then writes the results to disk and
// appends any language-server diagnostics so the model can self-correct.
package applypatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/patch"
	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// lspDiagnosticsTimeout bounds how long ApplyPatch waits for language servers to
// report diagnostics on the changed files before returning without them.
const lspDiagnosticsTimeout = 5 * time.Second

// Input is the structured input for the ApplyPatch tool.
type Input struct {
	// Patch is the full patch envelope, beginning with "*** Begin Patch" and
	// ending with "*** End Patch".
	Patch string `json:"patch" jsonschema:"required,description=A patch in the *** Begin Patch / *** End Patch envelope: *** Add File: / *** Update File: / *** Delete File: sections with @@ context anchors and space/-/+ hunk lines"`
}

// Tool implements tool.Tool for applying multi-file patches.
type Tool struct{}

// New returns a new ApplyPatch tool.
func New() *Tool { return &Tool{} }

// Name returns "ApplyPatch".
func (t *Tool) Name() string { return "ApplyPatch" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Apply a multi-file patch in one call. The patch is wrapped in '*** Begin Patch' / '*** End Patch' and contains one or more sections: " +
		"'*** Add File: path' followed by '+' lines; '*** Update File: path' (optionally '*** Move to: newpath') with '@@ context' anchors and hunk lines prefixed by a space (context), '-' (remove), or '+' (add); or '*** Delete File: path'. " +
		"Prefer this for changes spanning several files or several hunks; context lines are matched fuzzily."
}

// InputSchema returns the JSON schema for ApplyPatch's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns false — ApplyPatch mutates the filesystem.
func (t *Tool) ReadOnly() bool { return false }

// ConcurrencySafe returns false — patch application is not safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return false }

// CheckPermission mirrors the other mutating file tools: bypass/acceptEdits
// allow, plan denies, default asks, unknown fails closed (ask).
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	switch mode {
	case perm.ModeBypassPermissions, perm.ModeAcceptEdits:
		return perm.DecisionAllow
	case perm.ModePlan:
		return perm.DecisionDeny
	case perm.ModeDefault:
		return perm.DecisionAsk
	default:
		return perm.DecisionAsk
	}
}

// Execute parses the patch, reads the files it needs, applies it in memory, then
// writes the results to disk (adds/updates/moves/deletes). Parse/apply failures
// are soft errors surfaced to the model; a mid-write filesystem failure is also
// soft (the model sees what changed and what didn't).
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("applypatch: invalid input: %w", err)
	}
	if strings.TrimSpace(in.Patch) == "" {
		return tool.Result{Content: "patch is required", IsError: true}, nil
	}

	p, err := patch.Parse(in.Patch)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("invalid patch: %v", err), IsError: true}, nil
	}

	// Read the current content of the files the patch updates/deletes.
	current := make(map[string]string)
	for _, rel := range p.Files() {
		if data, rerr := os.ReadFile(t.abs(deps, rel)); rerr == nil {
			current[rel] = string(data)
		}
	}

	results, err := p.Apply(current)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("cannot apply patch: %v", err), IsError: true}, nil
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	changed := make(map[string]string) // path→content of written files, for diagnostics
	var summary []string
	for _, r := range results {
		switch r.Op {
		case patch.OpDelete:
			if err := os.Remove(t.abs(deps, r.Path)); err != nil && !os.IsNotExist(err) {
				return tool.Result{Content: fmt.Sprintf("cannot delete %s: %v", r.Path, err), IsError: true}, nil
			}
			summary = append(summary, "deleted "+r.Path)
		case patch.OpAdd, patch.OpUpdate:
			abs := t.abs(deps, r.Path)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return tool.Result{Content: fmt.Sprintf("cannot create parent dir for %s: %v", r.Path, err), IsError: true}, nil
			}
			if err := os.WriteFile(abs, []byte(r.Content), 0o644); err != nil {
				return tool.Result{Content: fmt.Sprintf("cannot write %s: %v", r.Path, err), IsError: true}, nil
			}
			// A move renames: drop the old path once the new one is written.
			switch {
			case r.OldPath != "" && r.OldPath != r.Path:
				if err := os.Remove(t.abs(deps, r.OldPath)); err != nil && !os.IsNotExist(err) {
					return tool.Result{Content: fmt.Sprintf("cannot remove moved-from %s: %v", r.OldPath, err), IsError: true}, nil
				}
				summary = append(summary, fmt.Sprintf("moved %s -> %s", r.OldPath, r.Path))
			case r.Op == patch.OpAdd:
				summary = append(summary, "added "+r.Path)
			default:
				summary = append(summary, "updated "+r.Path)
			}
			changed[abs] = r.Content
		}
	}
	sort.Strings(summary)

	out := fmt.Sprintf("applied patch: %d file(s)\n%s", len(results), strings.Join(summary, "\n"))
	if deps.LSP != nil && len(changed) > 0 {
		out += deps.LSP.ReportAfterChanges(ctx, changed, lspDiagnosticsTimeout)
	}
	return tool.Result{Content: out}, nil
}

// abs resolves a patch-relative path against the working directory.
func (t *Tool) abs(deps tool.Deps, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(deps.WorkDir, rel)
}
