// Package editmatch is a multi-strategy fuzzy string replacer used by the Edit
// tool. Instead of a single exact strings.Replace, it tries a cascade of
// matchers from exact to progressively looser, so a model's edit still applies
// when whitespace, indentation, or escaping has drifted from the file on disk.
package editmatch

import (
	"errors"
	"regexp"
	"strings"
)

// ErrNotFound means no strategy located oldStr in content.
var ErrNotFound = errors.New("old_string not found")

// ErrNotUnique means oldStr matched more than once and replaceAll is false.
var ErrNotUnique = errors.New("old_string is not unique")

// Similarity thresholds for anchor-based fuzzy matching. These values are
// tuned and intentionally kept identical to the upstream implementation.
const (
	blockAnchorSimilarityThreshold  = 0.65
	contextAwareSimilarityThreshold = 0.5
)

// replacer produces candidate substrings of content that may correspond to
// find. The driver tries each candidate in order until one is usable.
type replacer func(content, find string) []string

// strategies is the ordered cascade of matchers, from exact to fuzziest. The
// order is fixed for deterministic behavior; the first strategy that yields a
// usable match wins.
var strategies = []replacer{
	simpleReplacer,
	lineTrimmedReplacer,
	blockAnchorReplacer,
	whitespaceNormalizedReplacer,
	indentationFlexibleReplacer,
	escapeNormalizedReplacer,
	trimmedBoundaryReplacer,
	contextAwareReplacer,
	multiOccurrenceReplacer,
}

// Replace replaces oldStr with newStr in content using a cascade of matchers,
// from exact to progressively fuzzier. Strategies are tried in order; the first
// that yields a usable match wins.
//
//   - replaceAll=false: the chosen match must be unique within its strategy,
//     else ErrNotUnique; exactly one replacement is made.
//   - replaceAll=true: every occurrence the winning strategy finds is replaced.
//
// Returns the updated content and the number of replacements made. A fuzzy
// match wildly larger than oldStr is rejected (the disproportion guard) so a
// loose anchor cannot swallow unrelated code.
func Replace(content, oldStr, newStr string, replaceAll bool) (updated string, count int, err error) {
	if oldStr == "" {
		return content, 0, ErrNotFound
	}

	found := false
	for _, strat := range strategies {
		for _, search := range strat(content, oldStr) {
			index := strings.Index(content, search)
			if index == -1 {
				continue
			}
			found = true
			if isDisproportionateMatch(search, oldStr) {
				// A loose anchor matched a span far larger than oldStr;
				// refuse rather than risk eating unrelated code.
				return content, 0, ErrNotFound
			}
			if replaceAll {
				n := strings.Count(content, search)
				return strings.ReplaceAll(content, search, newStr), n, nil
			}
			if index != strings.LastIndex(content, search) {
				// Not unique under this candidate; keep looking.
				continue
			}
			return content[:index] + newStr + content[index+len(search):], 1, nil
		}
	}

	if !found {
		return content, 0, ErrNotFound
	}
	return content, 0, ErrNotUnique
}

// isDisproportionateMatch reports whether a fuzzy match is so much larger than
// oldStr that it likely swallowed unrelated code. The rule: reject when the
// match spans at least max(oldLines+3, oldLines*2) lines; for multi-line
// oldStr, also reject when the trimmed match length exceeds
// max(trimmedOld+500, trimmedOld*4).
func isDisproportionateMatch(search, oldStr string) bool {
	oldLines := strings.Count(oldStr, "\n") + 1
	searchLines := strings.Count(search, "\n") + 1
	if searchLines >= max(oldLines+3, oldLines*2) {
		return true
	}
	if oldLines == 1 {
		return false
	}
	oldTrim := len(strings.TrimSpace(oldStr))
	return len(strings.TrimSpace(search)) > max(oldTrim+500, oldTrim*4)
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// lineSimilarity returns 1 - distance/maxLen for two trimmed lines, treating a
// pair of empty lines as a perfect match.
func lineSimilarity(a, b string) float64 {
	maxLen := max(len(a), len(b))
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(maxLen)
}

// blockOffsets returns the byte range [start, end) covering originalLines from
// startLine to endLine inclusive, where each line is followed by a newline in
// the source content.
func blockOffsets(lines []string, startLine, endLine int) (int, int) {
	start := 0
	for k := 0; k < startLine; k++ {
		start += len(lines[k]) + 1
	}
	end := start
	for k := startLine; k <= endLine; k++ {
		end += len(lines[k])
		if k < endLine {
			end++
		}
	}
	return start, end
}

// simpleReplacer yields the find string verbatim (the exact-match strategy).
func simpleReplacer(_ string, find string) []string {
	return []string{find}
}

// lineTrimmedReplacer matches a block whose lines equal find's lines after
// trimming leading/trailing whitespace from each line.
func lineTrimmedReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) == 0 {
		return nil
	}

	var out []string
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true
		for j := 0; j < len(searchLines); j++ {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		start, end := blockOffsets(originalLines, i, i+len(searchLines)-1)
		out = append(out, content[start:end])
	}
	return out
}

// blockAnchorReplacer matches a multi-line block by anchoring on its first and
// last (trimmed) lines, then accepting the block whose middle lines are
// sufficiently similar (Levenshtein ratio >= blockAnchorSimilarityThreshold).
func blockAnchorReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if len(searchLines) < 3 {
		return nil
	}
	if searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) < 3 {
		return nil
	}

	firstLine := strings.TrimSpace(searchLines[0])
	lastLine := strings.TrimSpace(searchLines[len(searchLines)-1])
	searchBlockSize := len(searchLines)
	maxLineDelta := max(1, searchBlockSize/4)

	type candidate struct{ startLine, endLine int }
	var candidates []candidate
	for i := 0; i < len(originalLines); i++ {
		if strings.TrimSpace(originalLines[i]) != firstLine {
			continue
		}
		for j := i + 2; j < len(originalLines); j++ {
			if strings.TrimSpace(originalLines[j]) == lastLine {
				actualBlockSize := j - i + 1
				if abs(actualBlockSize-searchBlockSize) <= maxLineDelta {
					candidates = append(candidates, candidate{i, j})
				}
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	midSimilarity := func(c candidate) float64 {
		actualBlockSize := c.endLine - c.startLine + 1
		linesToCheck := min(searchBlockSize-2, actualBlockSize-2)
		if linesToCheck <= 0 {
			return 1
		}
		var similarity float64
		for j := 1; j < searchBlockSize-1 && j < actualBlockSize-1; j++ {
			orig := strings.TrimSpace(originalLines[c.startLine+j])
			srch := strings.TrimSpace(searchLines[j])
			if max(len(orig), len(srch)) == 0 {
				continue
			}
			similarity += lineSimilarity(orig, srch) / float64(linesToCheck)
		}
		return similarity
	}

	if len(candidates) == 1 {
		c := candidates[0]
		if midSimilarity(c) >= blockAnchorSimilarityThreshold {
			start, end := blockOffsets(originalLines, c.startLine, c.endLine)
			return []string{content[start:end]}
		}
		return nil
	}

	var best candidate
	maxSimilarity := -1.0
	for _, c := range candidates {
		if s := midSimilarity(c); s > maxSimilarity {
			maxSimilarity = s
			best = c
		}
	}
	if maxSimilarity >= blockAnchorSimilarityThreshold {
		start, end := blockOffsets(originalLines, best.startLine, best.endLine)
		return []string{content[start:end]}
	}
	return nil
}

var whitespaceRun = regexp.MustCompile(`\s+`)

func normalizeWhitespace(s string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// whitespaceNormalizedReplacer matches text that is identical once all runs of
// whitespace are collapsed to a single space, for single lines, substrings, and
// multi-line blocks.
func whitespaceNormalizedReplacer(content, find string) []string {
	normalizedFind := normalizeWhitespace(find)
	lines := strings.Split(content, "\n")

	var out []string
	for _, line := range lines {
		norm := normalizeWhitespace(line)
		if norm == normalizedFind {
			out = append(out, line)
			continue
		}
		if !strings.Contains(norm, normalizedFind) {
			continue
		}
		words := strings.Fields(strings.TrimSpace(find))
		if len(words) == 0 {
			continue
		}
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = regexp.QuoteMeta(w)
		}
		re, err := regexp.Compile(strings.Join(quoted, `\s+`))
		if err != nil {
			continue
		}
		if m := re.FindString(line); m != "" {
			out = append(out, m)
		}
	}

	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalizeWhitespace(block) == normalizedFind {
				out = append(out, block)
			}
		}
	}
	return out
}

// removeIndentation strips the common leading-whitespace prefix shared by all
// non-empty lines.
func removeIndentation(text string) string {
	lines := strings.Split(text, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t\r\n\v\f"))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return text
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = line[minIndent:]
	}
	return strings.Join(lines, "\n")
}

// indentationFlexibleReplacer matches a block that is identical to find once the
// common leading indentation is removed from both.
func indentationFlexibleReplacer(content, find string) []string {
	normalizedFind := removeIndentation(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")

	var out []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndentation(block) == normalizedFind {
			out = append(out, block)
		}
	}
	return out
}

// unescapeString resolves common backslash escape sequences to their literal
// characters.
func unescapeString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n', '\n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\'':
				b.WriteByte('\'')
			case '"':
				b.WriteByte('"')
			case '`':
				b.WriteByte('`')
			case '\\':
				b.WriteByte('\\')
			case '$':
				b.WriteByte('$')
			default:
				b.WriteByte('\\')
				continue
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// escapeNormalizedReplacer matches content where find contains backslash escape
// sequences (e.g. "\\n") that should be interpreted as literal characters.
func escapeNormalizedReplacer(content, find string) []string {
	unescapedFind := unescapeString(find)

	var out []string
	if strings.Contains(content, unescapedFind) {
		out = append(out, unescapedFind)
	}

	lines := strings.Split(content, "\n")
	findLines := strings.Split(unescapedFind, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if unescapeString(block) == unescapedFind {
			out = append(out, block)
		}
	}
	return out
}

// trimmedBoundaryReplacer matches when find differs only by leading/trailing
// whitespace around the whole string.
func trimmedBoundaryReplacer(content, find string) []string {
	trimmedFind := strings.TrimSpace(find)
	if trimmedFind == find {
		return nil
	}

	var out []string
	if strings.Contains(content, trimmedFind) {
		out = append(out, trimmedFind)
	}

	lines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmedFind {
			out = append(out, block)
		}
	}
	return out
}

// contextAwareReplacer matches a multi-line block anchored on its first and last
// (trimmed) lines when at least contextAwareSimilarityThreshold of the middle
// non-empty lines match exactly after trimming.
func contextAwareReplacer(content, find string) []string {
	findLines := strings.Split(find, "\n")
	if len(findLines) < 3 {
		return nil
	}
	if findLines[len(findLines)-1] == "" {
		findLines = findLines[:len(findLines)-1]
	}
	if len(findLines) < 3 {
		return nil
	}

	contentLines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(findLines[0])
	lastLine := strings.TrimSpace(findLines[len(findLines)-1])

	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != firstLine {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) != lastLine {
				continue
			}
			blockLines := contentLines[i : j+1]
			if len(blockLines) == len(findLines) {
				matching, totalNonEmpty := 0, 0
				for k := 1; k < len(blockLines)-1; k++ {
					bl := strings.TrimSpace(blockLines[k])
					fl := strings.TrimSpace(findLines[k])
					if bl != "" || fl != "" {
						totalNonEmpty++
						if bl == fl {
							matching++
						}
					}
				}
				if totalNonEmpty == 0 || float64(matching)/float64(totalNonEmpty) >= contextAwareSimilarityThreshold {
					return []string{strings.Join(blockLines, "\n")}
				}
			}
			break
		}
	}
	return nil
}

// multiOccurrenceReplacer yields the exact find string once; the driver handles
// multiple occurrences via replaceAll.
func multiOccurrenceReplacer(content, find string) []string {
	if !strings.Contains(content, find) {
		return nil
	}
	return []string{find}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
