package apiclient

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestToolFenceGrammar(t *testing.T) {
	t.Parallel()

	tools := []ToolDef{{Name: "Read"}, {Name: "Bash"}, {Name: "Grep"}}
	got := toolFenceGrammar(tools)

	want := "root ::= \"<tool:\" toolname \">\" object \"</tool>\"\n" +
		"toolname ::= \"Bash\" | \"Grep\" | \"Read\"\n" +
		jsonBodyGrammar
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("grammar mismatch (-want +got):\n%s", diff)
	}
}

func TestToolFenceGrammarSkipsUnquotableNames(t *testing.T) {
	t.Parallel()

	tools := []ToolDef{
		{Name: `bad"quote`},
		{Name: `bad\slash`},
		{Name: "  "},
		{Name: "Good"},
	}
	got := toolFenceGrammar(tools)
	if !strings.Contains(got, `toolname ::= "Good"`) {
		t.Errorf("expected only the quotable name, got:\n%s", got)
	}
}

func TestToolFenceGrammarEmpty(t *testing.T) {
	t.Parallel()

	if got := toolFenceGrammar(nil); got != "" {
		t.Errorf("no tools should yield no grammar, got %q", got)
	}
	if got := toolFenceGrammar([]ToolDef{{Name: `"`}}); got != "" {
		t.Errorf("no usable names should yield no grammar, got %q", got)
	}
}
