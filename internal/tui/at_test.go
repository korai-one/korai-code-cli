package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Nevaero/korai-code-cli/internal/apiclient"
)

func TestAtToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		value     string
		cursor    int
		wantOK    bool
		wantStart int
		wantQuery string
	}{
		{"start of input", "@mod", 4, true, 0, "mod"},
		{"after space", "see @mo", 7, true, 4, "mo"},
		{"bare at", "@", 1, true, 0, ""},
		{"email is not a mention", "user@host", 9, false, 0, ""},
		{"space ends token", "@mod ", 5, false, 0, ""},
		{"no at", "hello", 5, false, 0, ""},
		{"second mention", "@a @bc", 6, true, 3, "bc"},
		{"multibyte before token", "café @mo", 8, true, 5, "mo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ready(fakeRunner{})
			m.input.SetValue(tc.value)
			m.input.SetCursor(tc.cursor)
			start, query, ok := m.atToken()
			if ok != tc.wantOK || (ok && (start != tc.wantStart || query != tc.wantQuery)) {
				t.Errorf("atToken(%q@%d) = (%d,%q,%v), want (%d,%q,%v)",
					tc.value, tc.cursor, start, query, ok, tc.wantStart, tc.wantQuery, tc.wantOK)
			}
		})
	}
}

func TestFilterFiles(t *testing.T) {
	t.Parallel()
	files := []string{"main.go", "internal/tui/model.go", "internal/tui/menu.go", "README.md"}

	if got := filterFiles(files, ""); len(got) != len(files) {
		t.Errorf("empty query returned %d, want all %d", len(got), len(files))
	}
	got := filterFiles(files, "menu")
	if len(got) == 0 || got[0] != "internal/tui/menu.go" {
		t.Errorf(`filterFiles("menu") best = %v, want internal/tui/menu.go`, got)
	}
	if got := filterFiles(files, "zzzzz"); len(got) != 0 {
		t.Errorf("no-match query returned %d, want 0", len(got))
	}
}

// TestAtMenuOpensWhenFilesLoaded shows file suggestions once the list is in.
func TestAtMenuOpensWhenFilesLoaded(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.fileFinder = func() []string { return nil }
	m.files = []string{"main.go", "internal/tui/model.go"}
	m.filesLoaded = true

	tm, _ := m.Update(keyRunes("@"))
	m = tm.(Model)
	if len(m.atItems) != 2 {
		t.Fatalf("@ should list both files, got %d", len(m.atItems))
	}
}

// TestAtMenuLazyLoad requests the file list via a command on first "@".
func TestAtMenuLazyLoad(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.fileFinder = func() []string { return []string{"a.go", "b.go"} }

	tm, cmd := m.Update(keyRunes("@"))
	m = tm.(Model)
	if !m.filesLoading || cmd == nil {
		t.Fatal("first @ should kick off a file-list load command")
	}
	// Deliver the loaded list and confirm suggestions populate.
	tm, _ = m.Update(filesLoadedMsg{paths: []string{"a.go", "b.go"}})
	m = tm.(Model)
	if !m.filesLoaded || len(m.atItems) != 2 {
		t.Errorf("after load: loaded=%v items=%d, want loaded with 2 items", m.filesLoaded, len(m.atItems))
	}
}

// TestAtMenuAcceptInsertsPath replaces the token with the chosen path.
func TestAtMenuAcceptInsertsPath(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.fileFinder = func() []string { return nil }
	m.files = []string{"internal/tui/menu.go"}
	m.filesLoaded = true

	for _, r := range "@menu" {
		tm, _ := m.Update(keyRunes(string(r)))
		m = tm.(Model)
	}
	if len(m.atItems) == 0 {
		t.Fatal("@menu should match a file")
	}
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)
	if want := "@internal/tui/menu.go "; m.input.Value() != want {
		t.Errorf("after accept input = %q, want %q", m.input.Value(), want)
	}
	if len(m.atItems) != 0 {
		t.Error("menu should close after accepting")
	}
}

// TestMentionExpansionOnSubmit sends expanded text to the model while the
// transcript shows what was typed.
func TestMentionExpansionOnSubmit(t *testing.T) {
	t.Parallel()
	m := ready(fakeRunner{})
	m.mentionExpander = func(s string) string { return s + " [expanded]" }

	m.input.SetValue("look at @x")
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(Model)

	// Transcript keeps the typed text.
	var typed bool
	for _, e := range m.entries {
		if e.kind == kindUser && e.text == "look at @x" {
			typed = true
		}
	}
	if !typed {
		t.Error("transcript should show the typed prompt")
	}
	// History (sent to the model) carries the expanded text.
	last := m.history[len(m.history)-1]
	tb, ok := last.Content[0].(apiclient.TextBlock)
	if !ok || !strings.Contains(tb.Text, "[expanded]") {
		t.Errorf("sent message = %+v, want it to contain the expansion", last.Content[0])
	}
}
