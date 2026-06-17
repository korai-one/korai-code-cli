// Package grep implements the Grep tool: a read-only, hermetic content search
// that scans files for a Go regular expression and returns matching lines as
// "path:line:text". The search is pure Go (regexp + filepath.WalkDir) and never
// shells out to an external program, so it is deterministic and dependency-free.
package grep

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/Nevaero/korai-code-cli/internal/perm"
	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// maxMatches caps the number of matching lines collected in a single search so
// a pathological pattern over a large tree cannot produce an unbounded result.
const maxMatches = 1000

// maxLineBytes is the largest single line the scanner will read before giving
// up on a file; it lets the search tolerate very long lines (e.g. minified JS).
const maxLineBytes = 1024 * 1024

// Input is the structured input for the Grep tool.
type Input struct {
	// Pattern is the Go regular expression to search file contents for.
	Pattern string `json:"pattern" jsonschema:"required,description=Go regular expression"`
	// Path is the directory to search; defaults to the working directory.
	Path string `json:"path,omitempty" jsonschema:"description=directory to search, defaults to working directory"`
	// Glob, if set, restricts the search to files whose base name matches it (e.g. *.go).
	Glob string `json:"glob,omitempty" jsonschema:"description=only search files whose base name matches this glob, e.g. *.go"`
}

// Tool implements tool.Tool for regular-expression content search.
type Tool struct{}

// New returns a new Grep tool.
func New() *Tool { return &Tool{} }

// Name returns "Grep".
func (t *Tool) Name() string { return "Grep" }

// Description returns the model-facing prompt text for this tool.
func (t *Tool) Description(_ context.Context) string {
	return "Searches file contents for a regular expression and returns matching lines as \"path:line:text\"."
}

// InputSchema returns the JSON schema for Grep's input struct.
func (t *Tool) InputSchema() *jsonschema.Schema {
	return tool.Schema[Input]()
}

// ReadOnly returns true — Grep only reads files and never mutates state.
func (t *Tool) ReadOnly() bool { return true }

// ConcurrencySafe returns true — searching files is safe to parallelize.
func (t *Tool) ConcurrencySafe() bool { return true }

// CheckPermission always allows Grep regardless of permission mode, because
// searching file contents is a read-only operation.
func (t *Tool) CheckPermission(_ context.Context, _ json.RawMessage, mode perm.Mode) perm.Decision {
	_ = mode
	return perm.DecisionAllow
}

// Execute compiles Input.Pattern and walks the search root, returning matching
// lines as "relpath:lineno:text". Bad input (empty pattern, invalid regex, bad
// glob) yields a soft error Result; only malformed JSON is a hard error.
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage, deps tool.Deps) (tool.Result, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("grep: invalid input: %w", err)
	}
	if in.Pattern == "" {
		return tool.Result{Content: "pattern is required", IsError: true}, nil
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("invalid regular expression: %v", err),
			IsError: true,
		}, nil
	}

	// Validate the glob once up front so a malformed pattern fails fast rather
	// than per-file inside the walk.
	if in.Glob != "" {
		if _, err := filepath.Match(in.Glob, "probe"); err != nil {
			return tool.Result{
				Content: fmt.Sprintf("invalid glob %q: %v", in.Glob, err),
				IsError: true,
			}, nil
		}
	}

	root := in.Path
	if root == "" {
		root = deps.WorkDir
	} else if !filepath.IsAbs(root) {
		root = filepath.Join(deps.WorkDir, root)
	}

	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}

	matches := make([]string, 0, 64)
	truncated := false

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			// Unreadable entry: skip it rather than aborting the whole search.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if in.Glob != "" {
			ok, matchErr := filepath.Match(in.Glob, filepath.Base(path))
			if matchErr != nil {
				return matchErr
			}
			if !ok {
				return nil
			}
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}

		stop := scanFile(ctx, path, rel, re, &matches)
		if stop {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})

	if walkErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tool.Result{}, ctxErr
		}
		// A glob match error surfaced from the walk callback is bad input.
		return tool.Result{
			Content: fmt.Sprintf("search failed: %v", walkErr),
			IsError: true,
		}, nil
	}

	if len(matches) == 0 {
		return tool.Result{Content: "no matches found"}, nil
	}

	content := strings.Join(matches, "\n")
	if truncated {
		content += fmt.Sprintf("\n... (results truncated at %d matches)", maxMatches)
	}
	return tool.Result{Content: content}, nil
}

// scanFile reads path line by line, appending "rel:lineno:text" to matches for
// every line that re matches. It returns true if the global match cap was hit
// and the caller should stop walking. Files that cannot be opened are skipped.
func scanFile(ctx context.Context, path, rel string, re *regexp.Regexp, matches *[]string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only file; close error is irrelevant.

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	lineNo := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return false
		}
		lineNo++
		line := scanner.Text()
		if re.MatchString(line) {
			*matches = append(*matches, fmt.Sprintf("%s:%d:%s", rel, lineNo, line))
			if len(*matches) >= maxMatches {
				return true
			}
		}
	}
	return false
}
