package plan

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// plansDir is the workspace-relative directory approved plans are written to.
var plansDir = filepath.Join(".korai", "plans")

// savePlan writes an approved plan to .korai/plans/<slug>.md under workDir and
// returns the workspace-relative path, or "" if it could not be saved (a record
// of the plan is a convenience, never required, so failures are logged and
// swallowed). The filename is derived from the plan content — a slug of its
// first line plus a short content hash — so it is stable and free of any clock
// dependency.
func savePlan(workDir, plan string) string {
	if workDir == "" {
		return ""
	}
	rel := filepath.Join(plansDir, planFilename(plan))
	full := filepath.Join(workDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		slog.Warn("saving plan", "error", err)
		return ""
	}
	if err := os.WriteFile(full, []byte(plan), 0o644); err != nil {
		slog.Warn("saving plan", "error", err)
		return ""
	}
	return filepath.ToSlash(rel)
}

// planFilename derives a stable "<slug>-<hash>.md" name from the plan content.
func planFilename(plan string) string {
	slug := slugify(firstLine(plan))
	if slug == "" {
		slug = "plan"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(plan))
	return fmt.Sprintf("%s-%08x.md", slug, h.Sum32())
}

// firstLine returns the first non-blank line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// slugify lowercases s and reduces it to a short, filesystem-safe slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
