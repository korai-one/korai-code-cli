// Package glob implements the Glob tool: a read-only tool that finds files
// matching a glob pattern (with support for ** recursive matching) and returns
// the matching paths.
//
// Name mapping: the conceptual "GlobTool" becomes package glob, constructor
// glob.New, type glob.Tool.
package glob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// Input is the structured input for the Glob tool.
type Input struct {
	// Pattern is the glob pattern to match against, e.g. "**/*.go".
	Pattern string `json:"pattern" jsonschema:"required,description=Glob pattern to match against file paths, e.g. **/*.go (** matches across directory segments)"`
	// Path is the base directory to search; relative paths resolve against the
	// working directory. When empty the working directory is used.
	Path string `json:"path,omitempty" jsonschema:"description=Base directory to search; defaults to the working directory"`
}

// Tool implements tool.Tool for glob file-path matching.
type Tool struct{}

// New returns a new Glob tool.
func New() *Tool { return &Tool{} }

// Name returns "Glob".
func (t *Tool) Name() string { return "Glob" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Finds files matching a glob pattern (supports ** for recursive matching) and returns matching paths."
}

// InputSchema returns the JSON schema for Glob's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — Glob never mutates state.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — listing files is safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows Glob regardless of permission mode.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	// Listing matching file paths is always allowed in every permission mode.
	_ = mode
	return perm.DecisionAllow
}

// Execute walks the base directory and returns the sorted relative paths of the
// files matching Input.Pattern, joined by newlines. A malformed pattern or an
// empty pattern yields a soft error (IsError result, nil error); invalid JSON
// yields a hard error.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("glob: invalid input: %w", err)
	}
	if in.Pattern == "" {
		return tool.Result{Content: "pattern is required", IsError: true}, nil
	}

	base := deps.WorkDir
	if in.Path != "" {
		base = in.Path
		if !filepath.IsAbs(base) {
			base = filepath.Join(deps.WorkDir, in.Path)
		}
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	var matches []string
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			// Skip version-control metadata entirely.
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return relErr
		}
		// Normalize to forward slashes so the matcher is OS-independent.
		rel = filepath.ToSlash(rel)
		ok, matchErr := matchPattern(in.Pattern, rel)
		if matchErr != nil {
			return matchErr
		}
		if ok {
			matches = append(matches, rel)
		}
		return nil
	})

	if walkErr != nil {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		if errors.Is(walkErr, filepath.ErrBadPattern) {
			return tool.Result{
				Content: fmt.Sprintf("invalid glob pattern %q", in.Pattern),
				IsError: true,
			}, nil
		}
		return tool.Result{
			Content: fmt.Sprintf("cannot search %s: %v", base, walkErr),
			IsError: true,
		}, nil
	}

	if len(matches) == 0 {
		return tool.Result{Content: fmt.Sprintf("no files match pattern %q", in.Pattern)}, nil
	}

	sort.Strings(matches)
	return tool.Result{Content: strings.Join(matches, "\n")}, nil
}

// matchPattern reports whether name matches pattern, where pattern may contain
// "**" segments that match any number of path segments (including zero). Both
// pattern and name are slash-separated. Within a single segment, matching is
// delegated to filepath.Match (so "*", "?", and character classes behave per
// the stdlib). A malformed segment returns filepath.ErrBadPattern.
func matchPattern(pattern, name string) (bool, error) {
	pSegs := strings.Split(pattern, "/")
	nSegs := strings.Split(name, "/")
	return matchSegments(pSegs, nSegs)
}

// matchSegments performs the recursive "**"-aware segment match between the
// pattern segments p and the name segments n.
func matchSegments(p, n []string) (bool, error) {
	for len(p) > 0 {
		if p[0] == "**" {
			// Collapse consecutive "**" segments — they are equivalent to one.
			for len(p) > 1 && p[1] == "**" {
				p = p[1:]
			}
			rest := p[1:]
			// "**" matches zero or more name segments: try every split point.
			for i := 0; i <= len(n); i++ {
				ok, err := matchSegments(rest, n[i:])
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
			return false, nil
		}

		// A non-"**" pattern segment must consume exactly one name segment.
		if len(n) == 0 {
			return false, nil
		}
		ok, err := filepath.Match(p[0], n[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		p = p[1:]
		n = n[1:]
	}
	// Pattern exhausted: match iff the name is also fully consumed.
	return len(n) == 0, nil
}
