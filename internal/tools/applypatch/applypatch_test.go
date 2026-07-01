package applypatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/tool"
)

// TestExecuteAddAndUpdate drives the tool end-to-end: an Add and an Update in
// one patch are parsed, applied, and written to disk under the working dir.
func TestExecuteAddAndUpdate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patchText := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: new.txt",
		"+hello",
		"+world",
		"*** Update File: existing.txt",
		"@@",
		" line1",
		"-line2",
		"+line2-edited",
		" line3",
		"*** End Patch",
		"",
	}, "\n")

	raw, _ := json.Marshal(Input{Patch: patchText})
	res, err := New().Execute(context.Background(), raw, tool.Deps{WorkDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}

	added, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatalf("new.txt not created: %v", err)
	}
	if !strings.Contains(string(added), "hello") || !strings.Contains(string(added), "world") {
		t.Errorf("new.txt content = %q", added)
	}

	updated, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "line2-edited") || strings.Contains(string(updated), "\nline2\n") {
		t.Errorf("existing.txt not updated as expected:\n%s", updated)
	}
}

// TestExecuteBadPatch reports a malformed patch as a soft tool error, not a
// hard failure.
func TestExecuteBadPatch(t *testing.T) {
	raw, _ := json.Marshal(Input{Patch: "not a patch"})
	res, err := New().Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Execute returned a hard error: %v", err)
	}
	if !res.IsError {
		t.Error("expected a soft error result for a malformed patch")
	}
}

// TestExecuteEmptyPatch rejects an empty patch with a soft error.
func TestExecuteEmptyPatch(t *testing.T) {
	raw, _ := json.Marshal(Input{Patch: "  "})
	res, _ := New().Execute(context.Background(), raw, tool.Deps{WorkDir: t.TempDir()})
	if !res.IsError {
		t.Error("expected a soft error result for an empty patch")
	}
}
