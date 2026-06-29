package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditPreview(t *testing.T) {
	width := 60

	t.Run("edit shows old to new", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{
			"path": "a.go", "old_string": "foo()", "new_string": "bar()",
		})
		got := editPreview("Edit", in, width)
		if got == "" || !strings.Contains(got, "foo()") || !strings.Contains(got, "bar()") {
			t.Errorf("edit preview should diff old→new, got:\n%s", got)
		}
	})

	t.Run("write shows content as additions", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{"path": "n.txt", "content": "line1\nline2\n"})
		got := editPreview("Write", in, width)
		if got == "" || !strings.Contains(got, "line1") {
			t.Errorf("write preview should show new content, got:\n%s", got)
		}
	})

	t.Run("applypatch shows the patch", func(t *testing.T) {
		patch := "*** Begin Patch\n*** Update File: a.go\n@@\n-old\n+new\n*** End Patch"
		in, _ := json.Marshal(map[string]any{"patch": patch})
		got := editPreview("ApplyPatch", in, width)
		if !strings.Contains(got, "Update File: a.go") {
			t.Errorf("applypatch preview should show the patch, got:\n%s", got)
		}
	})

	t.Run("non-mutating tool has no preview", func(t *testing.T) {
		in, _ := json.Marshal(map[string]any{"command": "ls"})
		if got := editPreview("Bash", in, width); got != "" {
			t.Errorf("Bash should have no diff preview, got: %q", got)
		}
	})
}

func TestCapLines(t *testing.T) {
	if got := capLines("", 5); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
	s := "a\nb\nc\nd"
	if got := capLines(s, 10); got != s {
		t.Errorf("under cap unchanged, got %q", got)
	}
	got := capLines(s, 2)
	if !strings.HasPrefix(got, "a\nb") || !strings.Contains(got, "2 more lines") {
		t.Errorf("over cap should truncate + note, got %q", got)
	}
}
