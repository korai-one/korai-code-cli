package memory

import (
	"sort"
	"strings"
	"unicode"
)

// recall.go implements the lexical note-recall scoring: unpinned notes are
// ranked against the latest user message by stopword-stripped term overlap,
// tag matches, and recency. It is deliberately dependency-free (no embedding
// or index); the store is small enough that a linear scan per turn is cheap.

// stopwords are common English and French words excluded from scoring so that
// function words never create a match on their own.
var stopwords = func() map[string]struct{} {
	words := []string{
		// English
		"a", "an", "and", "are", "as", "at", "be", "but", "by", "can", "do",
		"does", "for", "from", "has", "have", "how", "if", "in", "into", "is",
		"it", "its", "me", "my", "no", "not", "of", "on", "or", "our", "so",
		"that", "the", "their", "them", "then", "there", "these", "they",
		"this", "to", "up", "use", "was", "we", "what", "when", "where",
		"which", "who", "why", "will", "with", "you", "your",
		// French
		"au", "aux", "avec", "ce", "ces", "cette", "dans", "de", "des", "du",
		"elle", "en", "est", "et", "il", "ils", "je", "la", "le", "les",
		"leur", "lui", "ma", "mais", "mes", "moi", "mon", "ne", "nos",
		"notre", "nous", "ou", "où", "par", "pas", "pour", "quand", "que",
		"quel", "quelle", "qui", "quoi", "sa", "se", "ses", "son", "sont",
		"sur", "ta", "tes", "toi", "ton", "tu", "un", "une", "vos", "votre",
		"vous", "à", "ça", "être",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}()

// tokenSet lowercases s, splits it on non-letter/digit runes, and returns the
// set of tokens at least two runes long that are not stopwords. Accented
// letters are preserved so French text tokenizes naturally.
func tokenSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			tok := string(cur)
			if _, stop := stopwords[tok]; !stop {
				out[tok] = struct{}{}
			}
		}
		cur = cur[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return out
}

// recallNotes scores the unpinned notes in notes against query and returns the
// matches in descending relevance order (ties: newest first). Pinned notes are
// excluded — they are always injected separately. An empty or all-stopword
// query recalls nothing.
func recallNotes(notes []Note, query string) []Note {
	q := tokenSet(query)
	if len(q) == 0 {
		return nil
	}
	type scored struct {
		note  Note
		score float64
		seq   int
	}
	var hits []scored
	for i, n := range notes {
		if n.Pinned {
			continue
		}
		overlap := 0
		for tok := range tokenSet(n.Text) {
			if _, ok := q[tok]; ok {
				overlap++
			}
		}
		tagHits := 0
		for _, tag := range n.Tags {
			for tok := range tokenSet(tag) {
				if _, ok := q[tok]; ok {
					tagHits++
				}
			}
		}
		if overlap+tagHits == 0 {
			continue
		}
		// Recency is a fractional tiebreaker only: it orders equally-relevant
		// notes newest-first but can never outrank a genuine term match.
		recency := 0.0
		if len(notes) > 1 {
			recency = 0.5 * float64(i) / float64(len(notes)-1)
		}
		hits = append(hits, scored{
			note:  n,
			score: float64(overlap) + 2*float64(tagHits) + recency,
			seq:   i,
		})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return hits[a].seq > hits[b].seq
	})
	out := make([]Note, len(hits))
	for i, h := range hits {
		out[i] = h.note
	}
	return out
}

// matchesKeyword reports whether keyword occurs in message as a whole word,
// case-insensitively. Word boundaries are any non-letter/digit rune — enough
// to catch "deploy" inside "how do I deploy this?" without matching
// "deployment"⊃"deploy" false negatives mattering in practice.
func matchesKeyword(message, keyword string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return false
	}
	haystack := strings.ToLower(message)
	from := 0
	for {
		idx := strings.Index(haystack[from:], needle)
		if idx < 0 {
			return false
		}
		idx += from
		beforeOK := idx == 0 || !isWordRune(rune(haystack[idx-1]))
		afterIdx := idx + len(needle)
		afterOK := afterIdx >= len(haystack) || !isWordRune(rune(haystack[afterIdx]))
		if beforeOK && afterOK {
			return true
		}
		from = idx + 1
	}
}

// isWordRune reports whether r is a word constituent for keyword-boundary
// purposes (ASCII letters, digits, underscore — keywords are expected to be
// simple identifiers).
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
