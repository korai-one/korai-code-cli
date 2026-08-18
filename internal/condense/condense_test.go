package condense_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/condense"
)

func TestApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter *condense.Filter
		tool   string
		in     string
		want   string
	}{
		{
			name:   "untargeted tool is passed through",
			filter: condense.New(condense.Config{}), // default tools = Bash only
			tool:   "ReadFile",
			in:     "a\na\na\na\na\na\na\na\n",
			want:   "a\na\na\na\na\na\na\na\n",
		},
		{
			name:   "empty content is passed through",
			filter: condense.New(condense.Config{}),
			tool:   "Bash",
			in:     "",
			want:   "",
		},
		{
			name:   "adjacent duplicates collapse with a count",
			filter: condense.New(condense.Config{}),
			tool:   "Bash",
			in:     "downloading\ndownloading\ndownloading\ndone\n",
			want:   "downloading  (×3)\ndone\n",
		},
		{
			name:   "distinct lines are left alone",
			filter: condense.New(condense.Config{}),
			tool:   "Bash",
			in:     "one\ntwo\nthree\n",
			want:   "one\ntwo\nthree\n",
		},
		{
			name:   "blank runs collapse without a count marker",
			filter: condense.New(condense.Config{}),
			tool:   "Bash",
			in:     "a\n\n\n\nb\n",
			want:   "a\n\nb\n",
		},
		{
			name:   "no trailing newline is preserved",
			filter: condense.New(condense.Config{}),
			tool:   "Bash",
			in:     "x\nx\nx\nx\nx",
			want:   "x  (×5)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.filter.Apply(tt.tool, tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Apply mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestApplyNeverEnlarges checks the safety invariant: the returned content is
// never longer than the input, even when the dedup/truncation markers would add
// characters (in which case the original is returned untouched).
func TestApplyNeverEnlarges(t *testing.T) {
	t.Parallel()

	f := condense.New(condense.Config{})
	// A single repeated pair: "a\na" — collapsing to "a  (×2)" would be longer,
	// so the original must be returned.
	in := "a\na\n"
	if got := f.Apply("Bash", in); len(got) > len(in) {
		t.Errorf("Apply enlarged output: in=%q got=%q", in, got)
	}
}

// TestApplyNilReceiver documents that a nil *Filter is a safe no-op, matching
// the "nil is a valid disabled filter" convention used elsewhere.
func TestApplyNilReceiver(t *testing.T) {
	t.Parallel()

	var f *condense.Filter
	in := "anything\ngoes\n"
	if got := f.Apply("Bash", in); got != in {
		t.Errorf("nil filter changed content: got %q want %q", got, in)
	}
}

// TestApplyTruncationCountsAll verifies the omitted count reflects the real
// number of dropped lines after dedup runs first.
func TestApplyTruncationCountsAll(t *testing.T) {
	t.Parallel()

	f := condense.New(condense.Config{MaxLines: 10, HeadLines: 3, TailLines: 3})
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	got := f.Apply("Bash", strings.Join(lines, "\n")+"\n")
	wantMarker := "… 94 lines omitted (condensed to save tokens; full output shown in the terminal) …"
	if !strings.Contains(got, wantMarker) {
		t.Errorf("missing exact omission marker %q, got:\n%s", wantMarker, got)
	}
	if !strings.HasPrefix(got, "line-0\nline-1\nline-2\n") {
		t.Errorf("head not preserved:\n%s", got)
	}
	if !strings.HasSuffix(got, "line-97\nline-98\nline-99\n") {
		t.Errorf("tail not preserved:\n%s", got)
	}
}

// TestCondenseBypassesToolGate verifies the ungated entry point: a tool Apply
// would spare (not in the configured set) is still reduced by Condense, while
// the never-enlarge and nil-receiver contracts hold.
func TestCondenseBypassesToolGate(t *testing.T) {
	t.Parallel()

	f := condense.New(condense.Config{MaxLines: 10, HeadLines: 3, TailLines: 3})
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%d", i)
	}
	in := strings.Join(lines, "\n") + "\n"

	if got := f.Apply("ReadFile", in); got != in {
		t.Fatalf("Apply gate should spare ReadFile output")
	}
	got := f.Condense(in)
	if len(got) >= len(in) {
		t.Errorf("Condense did not shrink gated-tool output")
	}
	if !strings.Contains(got, "lines omitted") {
		t.Errorf("missing omission marker:\n%s", got)
	}

	short := "tiny\n"
	if got := f.Condense(short); got != short {
		t.Errorf("Condense enlarged or changed already-small content: %q", got)
	}
	var nilF *condense.Filter
	if got := nilF.Condense(in); got != in {
		t.Error("nil filter must return content unchanged")
	}
}
