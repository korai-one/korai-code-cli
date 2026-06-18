package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/memory"
)

func TestAppendReadRoundTrip(t *testing.T) {
	t.Parallel()

	s := memory.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	if err := s.Append("first"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append("second"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := "first\nsecond\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("contents mismatch (-want +got):\n%s", diff)
	}
}

func TestAppendTrimsTrailingNewline(t *testing.T) {
	t.Parallel()

	s := memory.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	if err := s.Append("note\n\n"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if diff := cmp.Diff("note\n", got); diff != "" {
		t.Errorf("contents mismatch (-want +got):\n%s", diff)
	}
}

func TestReadMissingFile(t *testing.T) {
	t.Parallel()

	s := memory.NewStore(filepath.Join(t.TempDir(), "absent.md"))
	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read missing file returned error: %v", err)
	}
	if got != "" {
		t.Errorf("Read missing file = %q, want empty", got)
	}
}

func TestAppendEmptyNote(t *testing.T) {
	t.Parallel()

	s := memory.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	cases := []string{"", "   ", "\n", "\t \n"}
	for _, note := range cases {
		if err := s.Append(note); err == nil {
			t.Errorf("Append(%q) = nil, want error", note)
		}
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "" {
		t.Errorf("file should stay empty after rejected appends, got %q", got)
	}
}

func TestAppendCreatesParentDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "deeper", "MEMORY.md")
	s := memory.NewStore(path)
	if err := s.Append("hello"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file created at %s: %v", path, err)
	}
}

func TestCapEvictionDropsOldestWholeLines(t *testing.T) {
	t.Parallel()

	// Each line is "lineNN\n" = 7 bytes. Cap at 20 bytes holds at most 2 lines.
	s := memory.NewStoreWithCap(filepath.Join(t.TempDir(), "MEMORY.md"), 20)
	for i := 1; i <= 5; i++ {
		if err := s.Append("line0" + string(rune('0'+i))); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if len(got) > 20 {
		t.Errorf("contents = %d bytes, want <= 20", len(got))
	}
	want := "line04\nline05\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("eviction mismatch (-want +got):\n%s", diff)
	}
	// Never split a line: every retained line keeps its trailing newline and no
	// partial prefix survives.
	for _, line := range strings.SplitAfter(got, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, "\n") {
			t.Errorf("retained partial line %q without newline", line)
		}
	}
}

func TestCapKeepsSingleOversizeLineWhole(t *testing.T) {
	t.Parallel()

	s := memory.NewStoreWithCap(filepath.Join(t.TempDir(), "MEMORY.md"), 5)
	big := "this-single-line-is-way-over-cap"
	if err := s.Append(big); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if diff := cmp.Diff(big+"\n", got); diff != "" {
		t.Errorf("oversize single line should be kept whole (-want +got):\n%s", diff)
	}
}
