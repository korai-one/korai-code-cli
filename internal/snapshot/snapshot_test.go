package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// newManager builds a Manager over fresh temp dirs, skipping the test when git
// is unavailable so the suite stays green on machines without git.
func newManager(t *testing.T) (*Manager, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	worktree := t.TempDir()
	dataDir := t.TempDir()
	m := New(worktree, dataDir)
	if !m.Enabled() {
		t.Skip("git not available")
	}
	return m, worktree
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestSnapshotEmpty(t *testing.T) {
	m, _ := newManager(t)
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if id == "" {
		t.Fatal("expected a tree hash for an empty worktree, got empty string")
	}
}

func TestSnapshotNonEmpty(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "hello\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty tree hash")
	}
}

func TestRestoreModified(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "original\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	writeFile(t, wt, "a.txt", "changed\n")
	if err := m.Restore(context.Background(), id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, wt, "a.txt"); got != "original\n" {
		t.Errorf("content not reverted: got %q", got)
	}
}

func TestRestoreRemovesAdded(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "keep\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	writeFile(t, wt, "added.txt", "new\n")
	if err := m.Restore(context.Background(), id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "added.txt")); !os.IsNotExist(err) {
		t.Errorf("added file should have been removed, stat err = %v", err)
	}
	if got := readFile(t, wt, "a.txt"); got != "keep\n" {
		t.Errorf("pre-existing file disturbed: got %q", got)
	}
}

func TestRestoreRestoresDeleted(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "content\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if err := os.Remove(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := m.Restore(context.Background(), id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, wt, "a.txt"); got != "content\n" {
		t.Errorf("deleted file not restored: got %q", got)
	}
}

func TestDiffShowsModification(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "line one\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	writeFile(t, wt, "a.txt", "line two\n")
	diff, err := m.Diff(context.Background(), id)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "-line one") || !strings.Contains(diff, "+line two") {
		t.Errorf("diff missing expected hunks:\n%s", diff)
	}
}

func TestDiffIdenticalIsEmpty(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "same\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	diff, err := m.Diff(context.Background(), id)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != "" {
		t.Errorf("expected empty diff, got %q", diff)
	}
}

func TestTwoSnapshotsDiffer(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "first\n")
	first, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot 1: %v", err)
	}
	writeFile(t, wt, "a.txt", "second\n")
	second, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if first == second {
		t.Errorf("expected distinct ids, both = %q", first)
	}
}

func TestGitignoreExcluded(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, ".gitignore", "ignored/\n")
	writeFile(t, wt, "ignored/secret.txt", "nope\n")
	writeFile(t, wt, "tracked.txt", "yes\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Modifying an ignored file must not appear in the diff against the snapshot.
	writeFile(t, wt, "ignored/secret.txt", "changed\n")
	diff, err := m.Diff(context.Background(), id)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.Contains(diff, "secret.txt") {
		t.Errorf("ignored file leaked into diff:\n%s", diff)
	}
}

func TestRealGitUntouched(t *testing.T) {
	m, wt := newManager(t)
	// A snapshot must never create a .git directory inside the worktree.
	writeFile(t, wt, "a.txt", "x\n")
	if _, err := m.Snapshot(context.Background()); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); !os.IsNotExist(err) {
		t.Errorf("shadow git leaked a .git into the worktree, stat err = %v", err)
	}
}

func TestDisabledManagerNoOp(t *testing.T) {
	// A Manager pointed at a non-directory worktree is disabled and every method
	// is a safe no-op returning zero values and nil errors.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := New(f, t.TempDir())
	if m.Enabled() {
		t.Fatal("expected disabled Manager for a file worktree")
	}
	id, err := m.Snapshot(context.Background())
	if id != "" || err != nil {
		t.Errorf("Snapshot on disabled = (%q, %v)", id, err)
	}
	if err := m.Restore(context.Background(), "deadbeef"); err != nil {
		t.Errorf("Restore on disabled = %v", err)
	}
	diff, err := m.Diff(context.Background(), "deadbeef")
	if diff != "" || err != nil {
		t.Errorf("Diff on disabled = (%q, %v)", diff, err)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	m, wt := newManager(t)
	writeFile(t, wt, "a.txt", "a1\n")
	writeFile(t, wt, "dir/b.txt", "b1\n")
	id, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	writeFile(t, wt, "a.txt", "a2\n")    // modify
	writeFile(t, wt, "dir/c.txt", "c\n") // add
	if err := os.Remove(filepath.Join(wt, "dir/b.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := m.Restore(context.Background(), id); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got := map[string]string{
		"a.txt":     readFile(t, wt, "a.txt"),
		"dir/b.txt": readFile(t, wt, "dir/b.txt"),
	}
	want := map[string]string{"a.txt": "a1\n", "dir/b.txt": "b1\n"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("restored content mismatch (-want +got):\n%s", diff)
	}
	if _, err := os.Stat(filepath.Join(wt, "dir/c.txt")); !os.IsNotExist(err) {
		t.Errorf("added file dir/c.txt should be removed, stat err = %v", err)
	}
}
