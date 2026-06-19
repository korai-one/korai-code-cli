package tui

import (
	"strings"
	"testing"

	"github.com/Nevaero/korai-code-cli/internal/command"
)

// TestCommandMenuFilter covers the pure suggestion filter.
func TestCommandMenuFilter(t *testing.T) {
	t.Parallel()
	all := testCommands().All()

	// Expected count of name-prefix matches for "c", derived from the registry
	// so the test does not depend on exactly which built-ins are present.
	wantC := 0
	for _, c := range all {
		if strings.HasPrefix(c.Name(), "c") {
			wantC++
		}
	}

	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"slash lists all", "/", len(all)},
		{"prefix c", "/c", wantC},
		{"exact help", "/help", 1},
		{"no match", "/zzz", 0},
		{"space ends name typing", "/help ", 0},
		{"not a command", "hello", 0},
		{"empty", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := len(commandMenu(all, tc.input))
			if got != tc.want {
				t.Errorf("commandMenu(%q) = %d suggestions, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// TestCommandMenuCaseInsensitive matches regardless of case.
func TestCommandMenuCaseInsensitive(t *testing.T) {
	t.Parallel()
	all := testCommands().All()
	if got := len(commandMenu(all, "/HE")); got != 1 {
		t.Errorf(`commandMenu("/HE") = %d, want 1 (help)`, got)
	}
}

// TestCommandMenuFuzzySubsequence matches non-contiguous characters, not just
// prefixes — "hp" finds "help", "tls" finds "tools".
func TestCommandMenuFuzzySubsequence(t *testing.T) {
	t.Parallel()
	all := testCommands().All()
	cases := map[string]string{"/hp": "help", "/tls": "tools", "/qt": "quit"}
	for input, want := range cases {
		got := commandMenu(all, input)
		if len(got) == 0 || got[0].Name() != want {
			t.Errorf("commandMenu(%q) best match = %v, want %q", input, names(got), want)
		}
	}
}

func names(cs []command.Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}
