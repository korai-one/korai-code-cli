// Package repomap builds a ranked, budget-fitted map of a repository's source
// symbols. It walks the worktree, extracts each file's defined symbols and the
// identifiers it references (extract.go), builds a directed graph in which a
// file links to the files defining the symbols it uses, ranks files by PageRank
// (rank.go), and renders the highest-ranked files with their key symbols within
// a token budget. The model reads the map to understand the codebase layout
// without opening every file.
//
// The extractor is dependency-free (Go via go/ast, other languages via regex),
// so the map works on any machine with no toolchain beyond the Go stdlib. The
// extraction is a seam: a richer parser (e.g. tree-sitter) could replace it
// without changing the graph, ranking, or rendering.
package repomap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Default tuning for a map build.
const (
	defaultTokenBudget = 1024
	defaultMaxFiles    = 5000
	maxFileBytes       = 1 << 20 // skip files larger than 1 MiB (likely generated/data)
	maxSymbolsPerFile  = 40      // cap symbols rendered per file
	pageRankDamping    = 0.85
	pageRankIters      = 30
)

// sourceExts is the set of extensions the walker considers source code. The
// extractor returns nil for anything it cannot parse, so this is purely a
// walk-time filter to avoid reading binaries and data files.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true,
	".tsx": true, ".rs": true, ".java": true, ".rb": true, ".c": true,
	".h": true, ".cc": true, ".cpp": true, ".hpp": true,
}

// skipDirs are directory names never descended into during the walk.
var skipDirs = map[string]bool{
	".git": true, ".korai": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, "target": true, "bin": true, "obj": true,
	".idea": true, ".vscode": true, "testdata": true,
}

// Options tunes a single Build.
type Options struct {
	// TokenBudget is the approximate token budget for the rendered map. Zero
	// uses the default (1024). The top-ranked file is always included even if it
	// alone exceeds the budget, so the map is never empty when files exist.
	TokenBudget int
	// Mentioned lists files "in chat" — paths (relative to the root or absolute)
	// whose neighborhood is boosted in the ranking, so the map foregrounds code
	// the user is actively working with. May be nil.
	Mentioned []string
	// MaxFiles caps how many source files are walked. Zero uses the default.
	MaxFiles int
}

// Builder produces repo maps for a fixed root directory.
type Builder struct {
	root string
}

// New returns a Builder rooted at root (the repository worktree).
func New(root string) *Builder { return &Builder{root: root} }

// fileInfo is the extracted view of one source file.
type fileInfo struct {
	rel  string
	defs []Symbol
	refs []string
}

// Build walks the repository, ranks its files, and returns the rendered map.
// It returns an empty string (no error) when no source files are found.
func (b *Builder) Build(ctx context.Context, opts Options) (string, error) {
	if opts.TokenBudget <= 0 {
		opts.TokenBudget = defaultTokenBudget
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = defaultMaxFiles
	}

	paths, err := b.walk(opts.MaxFiles)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}

	infos, err := b.extractAll(ctx, paths)
	if err != nil {
		return "", err
	}

	ranked := rankFiles(infos, b.normalizeMentioned(opts.Mentioned))
	return renderMap(ranked, opts.TokenBudget), nil
}

// walk collects candidate source-file paths (relative to the root), skipping
// ignored directories, oversized files, and non-source extensions.
func (b *Builder) walk(maxFiles int) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(b.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, don't abort the whole walk
		}
		if d.IsDir() {
			name := d.Name()
			if p != b.root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxFileBytes {
			return nil
		}
		rel, rerr := filepath.Rel(b.root, p)
		if rerr != nil {
			rel = p
		}
		paths = append(paths, filepath.ToSlash(rel))
		if len(paths) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths) // stable input order → deterministic output
	return paths, nil
}

// extractAll reads and extracts every path concurrently, honoring ctx.
func (b *Builder) extractAll(ctx context.Context, paths []string) ([]fileInfo, error) {
	infos := make([]fileInfo, len(paths))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))
	// Each goroutine writes only its own index of infos, so no lock is needed.
	for i, rel := range paths {
		i, rel := i, rel
		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			data, err := os.ReadFile(filepath.Join(b.root, filepath.FromSlash(rel)))
			if err != nil {
				return nil // unreadable file: leave a zero fileInfo, skip it later
			}
			content := string(data)
			infos[i] = fileInfo{
				rel:  rel,
				defs: Definitions(rel, content),
				refs: References(rel, content),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return infos, nil
}

// normalizeMentioned maps the caller's mentioned paths (absolute or relative,
// any separator) to the slash-relative form used as graph node ids.
func (b *Builder) normalizeMentioned(mentioned []string) map[string]float64 {
	if len(mentioned) == 0 {
		return nil
	}
	out := make(map[string]float64, len(mentioned))
	for _, m := range mentioned {
		rel := m
		if filepath.IsAbs(m) {
			if r, err := filepath.Rel(b.root, m); err == nil {
				rel = r
			}
		}
		out[filepath.ToSlash(rel)] = 1.0
	}
	return out
}

// rankFiles builds the reference graph and returns files sorted by PageRank
// (descending), breaking ties by path for determinism. Files with no defined
// symbols are dropped — they add no value to the map.
func rankFiles(infos []fileInfo, personalization map[string]float64) []rankedFile {
	// defIndex maps a defined symbol name to the files that define it.
	defIndex := make(map[string][]string)
	for _, fi := range infos {
		if fi.rel == "" {
			continue
		}
		for _, s := range fi.defs {
			defIndex[s.Name] = append(defIndex[s.Name], fi.rel)
		}
	}

	g := NewGraph()
	for _, fi := range infos {
		if fi.rel == "" {
			continue
		}
		g.AddNode(fi.rel)
	}
	for _, fi := range infos {
		if fi.rel == "" {
			continue
		}
		for _, r := range fi.refs {
			definers := defIndex[r]
			if len(definers) == 0 {
				continue
			}
			// A name defined in many files is a weak signal; split its edge
			// weight across the definers so ubiquitous names don't dominate.
			w := 1.0 / float64(len(definers))
			for _, def := range definers {
				if def != fi.rel {
					g.AddEdge(fi.rel, def, w)
				}
			}
		}
	}

	scores := g.PageRank(pageRankDamping, pageRankIters, personalization)

	ranked := make([]rankedFile, 0, len(infos))
	for _, fi := range infos {
		if fi.rel == "" || len(fi.defs) == 0 {
			continue
		}
		ranked = append(ranked, rankedFile{path: fi.rel, symbols: fi.defs, score: scores[fi.rel]})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	return ranked
}
