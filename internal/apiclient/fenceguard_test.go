package apiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidToolInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", true},
		{"whitespace", "  \n ", true},
		{"empty object", "{}", true},
		{"object", `{"path":"a.go"}`, true},
		{"object with unknown fields", `{"path":"a.go","bogus":1}`, true},
		{"broken json", `{"path": oops}`, false},
		{"unterminated", `{"path":"a.go"`, false},
		{"array", `[1,2]`, false},
		{"string", `"hello"`, false},
		{"number", `42`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidToolInput(json.RawMessage(tt.in)); got != tt.want {
				t.Errorf("ValidToolInput(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMalformedFencesClean(t *testing.T) {
	t.Parallel()

	calls := []ToolCallCompleteEvent{
		{ID: "c1", Name: "Read", Input: json.RawMessage(`{"path":"a.go"}`)},
	}
	if frags := MalformedFences("Reading the file now.", calls); frags != nil {
		t.Errorf("clean turn reported malformed fragments: %v", frags)
	}
}

func TestMalformedFencesUnterminated(t *testing.T) {
	t.Parallel()

	text := `Let me check. <tool:Read>{"path": "a.go"`
	frags := MalformedFences(text, nil)
	if len(frags) != 1 {
		t.Fatalf("fragments = %d, want 1", len(frags))
	}
	if !strings.HasPrefix(frags[0], "<tool:Read>") {
		t.Errorf("fragment should start at the fence open, got %q", frags[0])
	}
}

func TestMalformedFencesInvalidBody(t *testing.T) {
	t.Parallel()

	calls := []ToolCallCompleteEvent{
		{ID: "c1", Name: "Read", Input: json.RawMessage(`{"path": oops}`)},
	}
	frags := MalformedFences("", calls)
	if len(frags) != 1 {
		t.Fatalf("fragments = %d, want 1", len(frags))
	}
	if want := `<tool:Read>{"path": oops}</tool>`; frags[0] != want {
		t.Errorf("fragment = %q, want %q", frags[0], want)
	}
}

func TestMalformedFencesBoth(t *testing.T) {
	t.Parallel()

	text := "prose <tool:Grep>{broken"
	calls := []ToolCallCompleteEvent{
		{ID: "c1", Name: "Read", Input: json.RawMessage(`[1]`)},
	}
	if frags := MalformedFences(text, calls); len(frags) != 2 {
		t.Errorf("fragments = %d, want 2 (text remnant + invalid body)", len(frags))
	}
}

func TestMalformedFencesTruncatesLongFragment(t *testing.T) {
	t.Parallel()

	text := "<tool:Read>" + strings.Repeat("x", 2*maxFragmentQuote)
	frags := MalformedFences(text, nil)
	if len(frags) != 1 {
		t.Fatalf("fragments = %d, want 1", len(frags))
	}
	if len(frags[0]) > maxFragmentQuote+len("…") {
		t.Errorf("fragment length = %d, want <= %d", len(frags[0]), maxFragmentQuote+len("…"))
	}
	if !strings.HasSuffix(frags[0], "…") {
		t.Error("truncated fragment should end with an ellipsis")
	}
}

func TestFenceCorrectionNotice(t *testing.T) {
	t.Parallel()

	notice := FenceCorrectionNotice([]string{`<tool:Read>{"path": oops}</tool>`})
	for _, want := range []string{
		"malformed tool call",
		`<tool:Read>{"path": oops}</tool>`,
		`<tool:tool_name>{"param": "value"}</tool>`,
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
}

func TestRenderAssistantTurnText(t *testing.T) {
	t.Parallel()

	calls := []ToolCallCompleteEvent{
		{ID: "c1", Name: "Read", Input: json.RawMessage(`{"path": oops}`)},
		{ID: "c2", Name: "Grep", Input: json.RawMessage(`{"q":"x"}`)},
	}
	got := RenderAssistantTurnText("some prose", calls)
	want := "some prose\n<tool:Read>{\"path\": oops}</tool>\n<tool:Grep>{\"q\":\"x\"}</tool>"
	if got != want {
		t.Errorf("RenderAssistantTurnText = %q, want %q", got, want)
	}

	// No prose: no leading newline.
	got = RenderAssistantTurnText("", calls[:1])
	if want := "<tool:Read>{\"path\": oops}</tool>"; got != want {
		t.Errorf("RenderAssistantTurnText (no prose) = %q, want %q", got, want)
	}
}
