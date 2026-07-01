package lsp

import (
	"strings"
	"testing"

	protocol "github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/google/go-cmp/cmp"
)

func diag(line, char uint32, sev protocol.DiagnosticSeverity, source, msg string) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: line, Character: char},
			End:   protocol.Position{Line: line, Character: char},
		},
		Severity: sev,
		Source:   source,
		Message:  msg,
	}
}

func TestSeverityLabel(t *testing.T) {
	cases := map[protocol.DiagnosticSeverity]string{
		protocol.SeverityError:         "Error",
		protocol.SeverityWarning:       "Warning",
		protocol.SeverityInformation:   "Info",
		protocol.SeverityHint:          "Hint",
		protocol.DiagnosticSeverity(0): "Error", // unset defaults to Error
	}
	for sev, want := range cases {
		if got := SeverityLabel(sev); got != want {
			t.Errorf("SeverityLabel(%d) = %q, want %q", sev, got, want)
		}
	}
}

func TestFormatDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		d    protocol.Diagnostic
		want string
	}{
		{
			name: "error with source",
			d:    diag(11, 4, protocol.SeverityError, "typescript", "undefined name"),
			want: "Error [12:5] undefined name (typescript)",
		},
		{
			name: "warning without source",
			d:    diag(0, 0, protocol.SeverityWarning, "", "unused variable"),
			want: "Warning [1:1] unused variable",
		},
		{
			name: "collapses newlines in message",
			d:    diag(2, 2, protocol.SeverityError, "", "line one\nline two\n  line three"),
			want: "Error [3:3] line one line two line three",
		},
		{
			name: "with string code",
			d: func() protocol.Diagnostic {
				d := diag(5, 5, protocol.SeverityError, "eslint", "bad")
				d.Code = "no-unused"
				return d
			}(),
			want: "Error [6:6] bad (eslint: no-unused)",
		},
		{
			name: "with numeric code and no source",
			d: func() protocol.Diagnostic {
				d := diag(5, 5, protocol.SeverityError, "", "bad")
				d.Code = 2304
				return d
			}(),
			want: "Error [6:6] bad (2304)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDiagnostic(tc.d); got != tc.want {
				t.Errorf("FormatDiagnostic() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReportEmpty(t *testing.T) {
	if got := Report("foo.go", nil); got != "" {
		t.Errorf("Report with no diags = %q, want empty", got)
	}
}

func TestReportSortsErrorsFirst(t *testing.T) {
	diags := []protocol.Diagnostic{
		diag(1, 0, protocol.SeverityWarning, "", "a warning"),
		diag(2, 0, protocol.SeverityError, "", "an error"),
		diag(3, 0, protocol.SeverityHint, "", "a hint"),
		diag(4, 0, protocol.SeverityInformation, "", "some info"),
	}
	got := Report("pkg/foo.go", diags)
	want := strings.Join([]string{
		`<file_diagnostics file="pkg/foo.go">`,
		"Error [3:1] an error",
		"Warning [2:1] a warning",
		"Info [5:1] some info",
		"Hint [4:1] a hint",
		"</file_diagnostics>",
	}, "\n")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Report mismatch (-want +got):\n%s", diff)
	}
}

func TestReportCapsAtTen(t *testing.T) {
	var diags []protocol.Diagnostic
	for i := 0; i < 15; i++ {
		diags = append(diags, diag(uint32(i), 0, protocol.SeverityError, "", "err"))
	}
	got := Report("foo.go", diags)
	lines := strings.Split(got, "\n")
	// open tag + 10 diag lines + "... and N more" + close tag = 13 lines
	if len(lines) != 13 {
		t.Fatalf("got %d lines, want 13:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "... and 5 more") {
		t.Errorf("expected '... and 5 more', got:\n%s", got)
	}
}

func TestReportProject(t *testing.T) {
	byFile := map[string][]protocol.Diagnostic{
		"a.go": {
			diag(0, 0, protocol.SeverityWarning, "", "warn in a"),
		},
		"b.go": {
			diag(4, 2, protocol.SeverityError, "", "err in b"),
		},
		"current.go": {
			diag(0, 0, protocol.SeverityError, "", "excluded"),
		},
	}
	got := ReportProject(byFile, "current.go")
	want := strings.Join([]string{
		"<project_diagnostics>",
		"b.go:5:3 Error err in b",
		"a.go:1:1 Warning warn in a",
		"</project_diagnostics>",
	}, "\n")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ReportProject mismatch (-want +got):\n%s", diff)
	}
}

func TestReportProjectEmpty(t *testing.T) {
	byFile := map[string][]protocol.Diagnostic{
		"only.go": {diag(0, 0, protocol.SeverityError, "", "x")},
	}
	if got := ReportProject(byFile, "only.go"); got != "" {
		t.Errorf("ReportProject excluding all = %q, want empty", got)
	}
	if got := ReportProject(nil, ""); got != "" {
		t.Errorf("ReportProject(nil) = %q, want empty", got)
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name   string
		byFile map[string][]protocol.Diagnostic
		want   string
	}{
		{
			name:   "empty",
			byFile: nil,
			want:   "",
		},
		{
			name: "errors and warnings",
			byFile: map[string][]protocol.Diagnostic{
				"a.go": {
					diag(0, 0, protocol.SeverityError, "", "e1"),
					diag(1, 0, protocol.SeverityError, "", "e2"),
					diag(2, 0, protocol.SeverityWarning, "", "w1"),
				},
			},
			want: "<diagnostic_summary>2 errors, 1 warnings</diagnostic_summary>",
		},
		{
			name: "only warnings omits errors",
			byFile: map[string][]protocol.Diagnostic{
				"a.go": {diag(0, 0, protocol.SeverityWarning, "", "w")},
			},
			want: "<diagnostic_summary>1 warnings</diagnostic_summary>",
		},
		{
			name: "info and hints do not count",
			byFile: map[string][]protocol.Diagnostic{
				"a.go": {
					diag(0, 0, protocol.SeverityInformation, "", "i"),
					diag(1, 0, protocol.SeverityHint, "", "h"),
				},
			},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Summary(tc.byFile); got != tc.want {
				t.Errorf("Summary() = %q, want %q", got, tc.want)
			}
		})
	}
}
