package repomap

import (
	"sort"
	"strings"
)

// rankedFile is one file's contribution to the map: its path, defined symbols,
// and PageRank score.
type rankedFile struct {
	path    string
	symbols []Symbol
	score   float64
}

// renderMap formats the ranked files into a compact, token-budgeted map. Files
// are emitted highest-rank first; emission stops once the running token
// estimate would exceed budget, except the first (top-ranked) file is always
// included so a non-empty input never yields an empty map.
//
// The shape is an aider-style outline:
//
//	<repo_map>
//	internal/engine/engine.go
//	  func New
//	  func (Engine) Run
//	  type Event
//	</repo_map>
func renderMap(ranked []rankedFile, budget int) string {
	if len(ranked) == 0 {
		return ""
	}

	const (
		header = "<repo_map>\n"
		footer = "</repo_map>\n"
	)
	var b strings.Builder
	b.WriteString(header)
	overhead := estimateTokens(header + footer)
	used := overhead

	for i, f := range ranked {
		block := renderFile(f)
		cost := estimateTokens(block)
		// Always include the top file; otherwise respect the budget.
		if i > 0 && used+cost > budget {
			break
		}
		b.WriteString(block)
		used += cost
	}
	b.WriteString(footer)
	return b.String()
}

// renderFile renders one file's header and its key symbols (exported first,
// then by source order), capped at maxSymbolsPerFile.
func renderFile(f rankedFile) string {
	syms := make([]Symbol, len(f.symbols))
	copy(syms, f.symbols)
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Exported != syms[j].Exported {
			return syms[i].Exported // exported symbols first
		}
		if syms[i].Line != syms[j].Line {
			return syms[i].Line < syms[j].Line
		}
		return syms[i].Name < syms[j].Name
	})
	if len(syms) > maxSymbolsPerFile {
		syms = syms[:maxSymbolsPerFile]
	}

	var b strings.Builder
	b.WriteString(f.path)
	b.WriteByte('\n')
	for _, s := range syms {
		b.WriteString("  ")
		b.WriteString(string(s.Kind))
		b.WriteByte(' ')
		b.WriteString(s.Name)
		b.WriteByte('\n')
	}
	return b.String()
}

// estimateTokens approximates the token count of s with the common ~4
// chars-per-token heuristic. It is only used to fit the map to a budget, so an
// approximation is fine.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}
