package config_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/config"
)

// requireBash skips the test when no bash is on PATH (e.g. a Windows CI runner
// without git-bash), so command-substitution cases stay cross-platform.
func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH; skipping command-substitution test")
	}
}

func TestResolveValueEnv(t *testing.T) {
	t.Setenv("FOO", "bar")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "braced", in: "x-${FOO}-y", want: "x-bar-y"},
		{name: "bare", in: "x-$FOO-y", want: "x-bar-y"},
		{name: "bare at end", in: "hello $FOO", want: "hello bar"},
		{name: "unset braced", in: "a${UNSET_KORAI_XYZ}b", want: "ab"},
		{name: "unset bare", in: "a-$UNSET_KORAI_XYZ", want: "a-"},
		{name: "literal trailing dollar", in: "cost is 5$", want: "cost is 5$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ResolveValue(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("ResolveValue(%q) returned error: %v", tt.in, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ResolveValue(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestResolveValueFastPath(t *testing.T) {
	// A string with no '$' is returned verbatim and spawns no subprocess.
	const in = "just a plain value with (parens) and {braces}"
	got, err := config.ResolveValue(context.Background(), in)
	if err != nil {
		t.Fatalf("ResolveValue returned error: %v", err)
	}
	if diff := cmp.Diff(in, got); diff != "" {
		t.Errorf("ResolveValue mismatch (-want +got):\n%s", diff)
	}
}

func TestResolveValueCommandSubstitution(t *testing.T) {
	requireBash(t)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "printf", in: "$(printf hello)", want: "hello"},
		{name: "echo trims newline", in: "$(echo hi)", want: "hi"},
		{name: "surrounded", in: "pre-$(echo hi)-post", want: "pre-hi-post"},
		{name: "nested parens", in: "$(echo $(echo deep))", want: "deep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ResolveValue(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("ResolveValue(%q) returned error: %v", tt.in, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ResolveValue(%q) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestResolveValueEnvAndCommand(t *testing.T) {
	requireBash(t)
	t.Setenv("FOO", "bar")

	const in = "${FOO}-$(echo hi)"
	const want = "bar-hi"
	got, err := config.ResolveValue(context.Background(), in)
	if err != nil {
		t.Fatalf("ResolveValue(%q) returned error: %v", in, err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ResolveValue(%q) mismatch (-want +got):\n%s", in, diff)
	}
}

func TestResolveValueCommandError(t *testing.T) {
	requireBash(t)

	tests := []struct {
		name string
		in   string
	}{
		{name: "non-zero exit", in: "$(exit 3)"},
		{name: "unknown command", in: "$(this-command-does-not-exist-korai-xyz)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ResolveValue(context.Background(), tt.in)
			if err == nil {
				t.Fatalf("ResolveValue(%q) = %q, want error", tt.in, got)
			}
		})
	}
}
