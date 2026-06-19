package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxWorkspaceFiles caps the @-mention candidate list so the picker stays
// responsive in very large trees.
const maxWorkspaceFiles = 5000

// maxMentionBytes caps how much of a referenced file is inlined into a prompt.
const maxMentionBytes = 64 * 1024

// skipDirs are directories never offered as @-mention candidates.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "bin": true,
	".korai": true, "dist": true, "build": true, ".idea": true, ".vscode": true,
}

// mentionRE matches an @-mention token: "@" at the start or after whitespace,
// followed by a run of path characters.
var mentionRE = regexp.MustCompile(`(^|\s)@([^\s]+)`)

// workspaceFiles returns a finder that lists workspace-relative, slash-separated
// file paths under wd (skipping VCS/build dirs), sorted, for @-mention
// completion. The walk is bounded by maxWorkspaceFiles.
func workspaceFiles(wd string) func() []string {
	return func() []string {
		var out []string
		_ = filepath.WalkDir(wd, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Skip unreadable entries and keep walking.
				return nil
			}
			if d.IsDir() {
				if path != wd && skipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(wd, path)
			if relErr != nil {
				return nil
			}
			out = append(out, filepath.ToSlash(rel))
			if len(out) >= maxWorkspaceFiles {
				return fs.SkipAll
			}
			return nil
		})
		sort.Strings(out)
		return out
	}
}

// mentionExpander returns a function that appends the contents of @-referenced
// files (resolved under wd) to a prompt, so the model receives the file rather
// than just its path. Missing or unreadable mentions are skipped; content is
// truncated to maxMentionBytes.
func mentionExpander(wd string) func(string) string {
	return func(prompt string) string {
		matches := mentionRE.FindAllStringSubmatch(prompt, -1)
		if len(matches) == 0 {
			return prompt
		}
		seen := make(map[string]bool)
		var b strings.Builder
		b.WriteString(prompt)
		for _, mt := range matches {
			rel := mt[2]
			if seen[rel] {
				continue
			}
			seen[rel] = true
			full := filepath.Join(wd, filepath.FromSlash(rel))
			data, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			truncated := false
			if len(data) > maxMentionBytes {
				data = data[:maxMentionBytes]
				truncated = true
			}
			fmt.Fprintf(&b, "\n\n--- referenced file: %s ---\n%s", rel, data)
			if truncated {
				b.WriteString("\n--- (truncated) ---")
			}
		}
		return b.String()
	}
}
