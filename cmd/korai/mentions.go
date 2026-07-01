package main

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

// imageMediaTypes maps an image file extension to its MIME type. An @-mention of
// one of these is attached as an image (for vision models), not inlined as text.
var imageMediaTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// maxImageBytes caps an attached image so a huge file can't blow up the request.
const maxImageBytes = 8 * 1024 * 1024

// imageMediaType returns the MIME type for an image path; ok is false otherwise.
func imageMediaType(rel string) (string, bool) {
	mt, ok := imageMediaTypes[strings.ToLower(filepath.Ext(rel))]
	return mt, ok
}

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
			if _, isImg := imageMediaType(rel); isImg {
				continue // images are attached as image blocks, not inlined as text
			}
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

// imageAttacher returns a function that reads each @-mentioned image file and
// returns it as an apiclient.ImageBlock (a base64 data URI), so vision-capable
// models receive the image itself. Non-image mentions are ignored here (the
// text mentionExpander inlines those). Unreadable, empty, or oversized files
// are skipped silently — a bad attachment must not break the turn.
func imageAttacher(wd string) func(string) []apiclient.ImageBlock {
	return func(prompt string) []apiclient.ImageBlock {
		matches := mentionRE.FindAllStringSubmatch(prompt, -1)
		if len(matches) == 0 {
			return nil
		}
		var out []apiclient.ImageBlock
		seen := make(map[string]bool)
		for _, mt := range matches {
			rel := mt[2]
			if seen[rel] {
				continue
			}
			seen[rel] = true
			mediaType, ok := imageMediaType(rel)
			if !ok {
				continue
			}
			data, err := os.ReadFile(filepath.Join(wd, filepath.FromSlash(rel)))
			if err != nil || len(data) == 0 || len(data) > maxImageBytes {
				continue
			}
			out = append(out, apiclient.ImageBlock{
				Source: "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data),
			})
		}
		return out
	}
}
