package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	mustWrite(t, filepath.Join(dir, "pkg", "util.go"), "package pkg")
	mustWrite(t, filepath.Join(dir, ".git", "config"), "[core]")
	mustWrite(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "x")

	got := workspaceFiles(dir)()

	want := map[string]bool{"main.go": true, "pkg/util.go": true}
	for _, p := range got {
		if strings.Contains(p, ".git") || strings.Contains(p, "node_modules") {
			t.Errorf("skipped dir leaked into results: %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing expected files: %v (got %v)", want, got)
	}
}

func TestMentionExpander(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "notes.txt"), "hello from notes")
	expand := mentionExpander(dir)

	got := expand("please read @notes.txt and summarize")
	if !strings.Contains(got, "please read @notes.txt") {
		t.Error("expansion should preserve the original prompt")
	}
	if !strings.Contains(got, "hello from notes") {
		t.Errorf("expansion should inline file content, got:\n%s", got)
	}
	if !strings.Contains(got, "referenced file: notes.txt") {
		t.Error("expansion should label the referenced file")
	}
}

func TestMentionExpanderSkipsMissing(t *testing.T) {
	dir := t.TempDir()
	expand := mentionExpander(dir)
	in := "read @does-not-exist.txt"
	if got := expand(in); got != in {
		t.Errorf("missing mention should leave the prompt unchanged, got %q", got)
	}
}

func TestMentionExpanderNoMentions(t *testing.T) {
	expand := mentionExpander(t.TempDir())
	in := "just a normal prompt"
	if got := expand(in); got != in {
		t.Errorf("expander changed a prompt with no mentions: %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
