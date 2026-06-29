package patch

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// wrap builds a complete envelope around a patch body.
func wrap(body string) string {
	return "*** Begin Patch\n" + body + "\n*** End Patch"
}

func mustParse(t *testing.T, text string) *Patch {
	t.Helper()
	p, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	return p
}

func TestAddFile(t *testing.T) {
	p := mustParse(t, wrap("*** Add File: path/new.go\n+line one\n+line two"))

	if got := p.Files(); len(got) != 0 {
		t.Errorf("Files() = %v, want empty (Add targets excluded)", got)
	}

	got, err := p.Apply(map[string]string{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "path/new.go", Op: OpAdd, Content: "line one\nline two\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateExactContext(t *testing.T) {
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n foo\n-bar\n+baz"))

	if diff := cmp.Diff([]string{"a.txt"}, p.Files()); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}

	got, err := p.Apply(map[string]string{"a.txt": "foo\nbar\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "a.txt", Op: OpUpdate, Content: "foo\nbaz\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateTrailingWhitespaceFuzzy(t *testing.T) {
	// Source has trailing whitespace the patch omits.
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n foo\n-bar\n+baz"))

	got, err := p.Apply(map[string]string{"a.txt": "foo   \nbar\t\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The located context line is rewritten with the patch's (clean) version.
	want := []Result{{Path: "a.txt", Op: OpUpdate, Content: "foo\nbaz\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateLeadingWhitespaceFuzzy(t *testing.T) {
	// Source is indented; the patch context is not. The all-whitespace-trim
	// pass should still locate it.
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n foo\n-bar\n+baz"))

	got, err := p.Apply(map[string]string{"a.txt": "    foo\n   bar\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "a.txt", Op: OpUpdate, Content: "foo\nbaz\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestUpdateUnicodeNormalizationFuzzy(t *testing.T) {
	// Source uses an EN DASH; the patch uses an ASCII hyphen.
	p := mustParse(t, wrap("*** Update File: a.py\n@@\n-import x  # a - b\n+import x  # changed"))

	got, err := p.Apply(map[string]string{"a.py": "import x  # a – b\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "a.py", Op: OpUpdate, Content: "import x  # changed\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestMultipleHunksReverseApply(t *testing.T) {
	// Edits near the top and bottom of the file must both land correctly even
	// though applying the top edit would otherwise shift the bottom one.
	body := "*** Update File: m.txt\n" +
		"@@\n foo\n-bar\n+BAR\n" +
		"@@\n baz\n-qux\n+QUX"
	p := mustParse(t, wrap(body))

	got, err := p.Apply(map[string]string{"m.txt": "foo\nbar\nbaz\nqux\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "m.txt", Op: OpUpdate, Content: "foo\nBAR\nbaz\nQUX\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestIdenticalSnippetsDisambiguatedByOrder(t *testing.T) {
	// Two identical "value = 1" lines. The ordered cursor must edit the first
	// occurrence first and the second occurrence second.
	body := "*** Update File: d.txt\n" +
		"@@ func a\n-value = 1\n+value = 10\n" +
		"@@ func b\n-value = 1\n+value = 20"
	p := mustParse(t, wrap(body))

	src := "func a\nvalue = 1\nfunc b\nvalue = 1\n"
	got, err := p.Apply(map[string]string{"d.txt": src})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "d.txt", Op: OpUpdate, Content: "func a\nvalue = 10\nfunc b\nvalue = 20\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestEndOfFileInsertion(t *testing.T) {
	body := "*** Update File: e.txt\n@@\n+tail\n*** End of File"
	p := mustParse(t, wrap(body))

	got, err := p.Apply(map[string]string{"e.txt": "a\nb\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "e.txt", Op: OpUpdate, Content: "a\nb\ntail\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestDeleteFile(t *testing.T) {
	p := mustParse(t, wrap("*** Delete File: old.txt"))

	if diff := cmp.Diff([]string{"old.txt"}, p.Files()); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}

	got, err := p.Apply(map[string]string{"old.txt": "whatever\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "old.txt", Op: OpDelete, Content: ""}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestMove(t *testing.T) {
	body := "*** Update File: src.txt\n*** Move to: dst.txt\n@@\n-line\n+line2"
	p := mustParse(t, wrap(body))

	// The read set is the original (source) path.
	if diff := cmp.Diff([]string{"src.txt"}, p.Files()); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}

	got, err := p.Apply(map[string]string{"src.txt": "line\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "dst.txt", OldPath: "src.txt", Op: OpUpdate, Content: "line2\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestMultiFileEnvelopeAndReadSet(t *testing.T) {
	body := "*** Add File: add.txt\n+hi\n" +
		"*** Delete File: del.txt\n" +
		"*** Update File: up.txt\n@@\n-x\n+y"
	p := mustParse(t, wrap(body))

	if diff := cmp.Diff([]string{"del.txt", "up.txt"}, p.Files()); diff != "" {
		t.Errorf("Files mismatch (-want +got):\n%s", diff)
	}

	got, err := p.Apply(map[string]string{"del.txt": "bye\n", "up.txt": "x\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{
		{Path: "add.txt", Op: OpAdd, Content: "hi\n"},
		{Path: "del.txt", Op: OpDelete, Content: ""},
		{Path: "up.txt", Op: OpUpdate, Content: "y\n"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestCRLFTolerated(t *testing.T) {
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n foo\n-bar\n+baz"))

	got, err := p.Apply(map[string]string{"a.txt": "foo\r\nbar\r\n"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// CRLF is normalized to LF.
	want := []Result{{Path: "a.txt", Op: OpUpdate, Content: "foo\nbaz\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestMissingTrailingNewlineTolerated(t *testing.T) {
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n foo\n-bar\n+baz"))

	got, err := p.Apply(map[string]string{"a.txt": "foo\nbar"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []Result{{Path: "a.txt", Op: OpUpdate, Content: "foo\nbaz\n"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Apply mismatch (-want +got):\n%s", diff)
	}
}

func TestErrorContextNotFound(t *testing.T) {
	p := mustParse(t, wrap("*** Update File: a.txt\n@@\n-nope\n+x"))

	_, err := p.Apply(map[string]string{"a.txt": "foo\nbar\n"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "a.txt") ||
		!strings.Contains(err.Error(), "failed to find") {
		t.Errorf("error %q lacks path or hint", err)
	}
}

func TestErrorAddOverExisting(t *testing.T) {
	p := mustParse(t, wrap("*** Add File: a.txt\n+hi"))

	_, err := p.Apply(map[string]string{"a.txt": "exists\n"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want already-exists error, got %v", err)
	}
}

func TestErrorUpdateMissing(t *testing.T) {
	p := mustParse(t, wrap("*** Update File: gone.txt\n@@\n-a\n+b"))

	_, err := p.Apply(map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("want missing-file error, got %v", err)
	}
}

func TestErrorDeleteMissing(t *testing.T) {
	p := mustParse(t, wrap("*** Delete File: gone.txt"))

	_, err := p.Apply(map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("want missing-file error, got %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse("not a patch"); err == nil {
		t.Error("expected error for missing begin marker")
	}
	if _, err := Parse("*** Begin Patch\n*** Add File: x\n+y"); err == nil {
		t.Error("expected error for missing end marker")
	}
	if _, err := Parse(wrap("*** Update File: empty.txt")); err == nil {
		t.Error("expected error for empty update hunk")
	}
}
