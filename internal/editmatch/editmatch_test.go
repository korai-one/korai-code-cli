package editmatch

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExactMatch(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	got, count, err := Replace(content, "beta", "BETA", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if diff := cmp.Diff("alpha\nBETA\ngamma\n", got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestLeadingIndentationDrift(t *testing.T) {
	content := "func f() {\n        return 1\n}\n"
	// The model supplied a different indentation level for the body.
	old := "func f() {\n    return 1\n}"
	got, count, err := Replace(content, old, "func f() {\n    return 2\n}", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if !strings.Contains(got, "return 2") {
		t.Errorf("expected body replaced, got:\n%s", got)
	}
}

func TestTrailingWhitespaceDrift(t *testing.T) {
	// Content has trailing spaces the model did not include.
	content := "a := 1   \nb := 2\n"
	got, count, err := Replace(content, "a := 1", "a := 99", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if !strings.Contains(got, "a := 99") {
		t.Errorf("expected replacement, got:\n%s", got)
	}
}

func TestBlockAnchorMiddleDrift(t *testing.T) {
	content := strings.Join([]string{
		"function compute(values) {",
		"  const total = values.reduce((a, b) => a + b, 0);",
		"  return total;",
		"}",
	}, "\n")
	// First and last lines anchor; the middle line drifted slightly (a vs acc).
	old := strings.Join([]string{
		"function compute(values) {",
		"  const total = values.reduce((acc, b) => acc + b, 0);",
		"  return total;",
		"}",
	}, "\n")
	got, count, err := Replace(content, old, "function compute(v) { return 0; }", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if diff := cmp.Diff("function compute(v) { return 0; }", got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestNotFound(t *testing.T) {
	content := "hello world\n"
	_, count, err := Replace(content, "this text is absent", "x", false)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestNotUnique(t *testing.T) {
	content := "x := 1\ny := 2\nx := 1\n"
	_, count, err := Replace(content, "x := 1", "z := 1", false)
	if !errors.Is(err, ErrNotUnique) {
		t.Errorf("err = %v, want ErrNotUnique", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestReplaceAll(t *testing.T) {
	content := "x := 1\ny := 2\nx := 1\n"
	got, count, err := Replace(content, "x := 1", "z := 1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if diff := cmp.Diff("z := 1\ny := 2\nz := 1\n", got); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestDisproportionGuard(t *testing.T) {
	// A tiny one-line oldStr that fuzzy strategies might try to expand into a
	// huge region must be rejected rather than swallowing the file. Here a
	// short anchor would otherwise match a span of many lines.
	var sb strings.Builder
	sb.WriteString("BEGIN\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("noise line of code here\n")
	}
	sb.WriteString("END\n")
	content := sb.String()

	// oldStr is a 3-line anchor block whose body bears no resemblance to the
	// 52-line region between BEGIN and END; if matched it would be wildly
	// disproportionate.
	old := "BEGIN\nsingle middle line\nEND"
	_, count, err := Replace(content, old, "REPLACED", false)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (disproportion guard)", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestEmptyOldString(t *testing.T) {
	_, count, err := Replace("abc", "", "x", false)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
