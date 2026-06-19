package tui

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestComputeDiff(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     []diffLine
	}{
		{
			name: "identical",
			old:  "a\nb\nc",
			new:  "a\nb\nc",
			want: []diffLine{
				{diffContext, "a"},
				{diffContext, "b"},
				{diffContext, "c"},
			},
		},
		{
			name: "pure addition at end",
			old:  "a\nb",
			new:  "a\nb\nc",
			want: []diffLine{
				{diffContext, "a"},
				{diffContext, "b"},
				{diffAdd, "c"},
			},
		},
		{
			name: "pure deletion at end",
			old:  "a\nb\nc",
			new:  "a\nb",
			want: []diffLine{
				{diffContext, "a"},
				{diffContext, "b"},
				{diffDel, "c"},
			},
		},
		{
			name: "replacement is del then add",
			old:  "a\nX\nc",
			new:  "a\nY\nc",
			want: []diffLine{
				{diffContext, "a"},
				{diffDel, "X"},
				{diffAdd, "Y"},
				{diffContext, "c"},
			},
		},
		{
			name: "interleaved changes",
			old:  "a\nb\nc\nd",
			new:  "a\nB\nc\nD",
			want: []diffLine{
				{diffContext, "a"},
				{diffDel, "b"},
				{diffAdd, "B"},
				{diffContext, "c"},
				{diffDel, "d"},
				{diffAdd, "D"},
			},
		},
		{
			name: "insertion in middle",
			old:  "a\nc",
			new:  "a\nb\nc",
			want: []diffLine{
				{diffContext, "a"},
				{diffAdd, "b"},
				{diffContext, "c"},
			},
		},
		{
			name: "empty old is all add",
			old:  "",
			new:  "a\nb",
			want: []diffLine{
				{diffDel, ""},
				{diffAdd, "a"},
				{diffAdd, "b"},
			},
		},
		{
			name: "empty new is all del",
			old:  "a\nb",
			new:  "",
			want: []diffLine{
				{diffDel, "a"},
				{diffDel, "b"},
				{diffAdd, ""},
			},
		},
		{
			name: "both empty",
			old:  "",
			new:  "",
			want: []diffLine{
				{diffContext, ""},
			},
		},
		{
			name: "trailing newline adds empty line",
			old:  "a\nb",
			new:  "a\nb\n",
			want: []diffLine{
				{diffContext, "a"},
				{diffContext, "b"},
				{diffAdd, ""},
			},
		},
		{
			name: "removed trailing newline",
			old:  "a\nb\n",
			new:  "a\nb",
			want: []diffLine{
				{diffContext, "a"},
				{diffContext, "b"},
				{diffDel, ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDiff(tt.old, tt.new)
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(diffLine{})); diff != "" {
				t.Errorf("computeDiff(%q, %q) mismatch (-want +got):\n%s", tt.old, tt.new, diff)
			}
		})
	}
}

func TestRenderDiffIdenticalReturnsEmpty(t *testing.T) {
	if got := renderDiff("a\nb\nc", "a\nb\nc", 80); got != "" {
		t.Errorf("renderDiff on identical input = %q, want empty", got)
	}
}

func TestRenderDiffContainsLineTexts(t *testing.T) {
	out := renderDiff("a\nX\nc", "a\nY\nc", 80)
	for _, want := range []string{"X", "Y", "a", "c"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderDiff output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRenderDiffCollapsesLongContext(t *testing.T) {
	var oldLines []string
	for i := 0; i < 200; i++ {
		oldLines = append(oldLines, "line")
	}
	old := strings.Join(oldLines, "\n")
	// Change only the first line; the rest is a long context run.
	newLines := append([]string{"changed"}, oldLines[1:]...)
	new := strings.Join(newLines, "\n")

	out := renderDiff(old, new, 80)
	if !strings.Contains(out, "unchanged") {
		t.Errorf("expected collapsed-context marker in output; got:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > maxDiffLines {
		t.Errorf("collapsed output has %d lines, want <= %d", got, maxDiffLines)
	}
}
