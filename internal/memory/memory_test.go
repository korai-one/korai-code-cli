package memory_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/memory"
)

var update = flag.Bool("update", false, "update golden files")

func newStore(t *testing.T) *memory.Store {
	t.Helper()
	return memory.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
}

func TestLegacyFileParsesAsPinnedNotes(t *testing.T) {
	t.Parallel()

	f := memory.Parse("first note\nsecond note\n\nthird note\n")
	if len(f.Facts) != 0 {
		t.Fatalf("legacy file yielded %d facts, want 0", len(f.Facts))
	}
	want := []memory.Note{
		{Text: "first note", Pinned: true},
		{Text: "second note", Pinned: true},
		{Text: "third note", Pinned: true},
	}
	if diff := cmp.Diff(want, f.Notes); diff != "" {
		t.Errorf("legacy notes mismatch (-want +got):\n%s", diff)
	}
}

func TestParseMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	f := memory.File{
		Facts: []memory.Fact{
			{Key: "editor", Value: "neovim", Pinned: true},
			{Key: "deploy", Value: "fly.io", Keywords: []string{"deploy", "fly"}},
		},
		Notes: []memory.Note{
			{Text: "prefers table-driven tests", Tags: []string{"testing"}},
			{Text: "migrated legacy line", Pinned: true},
			{Text: "often recalled", Uses: 3},
		},
	}
	got := memory.Parse(f.Marshal())
	if diff := cmp.Diff(f, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestParseKeepsUnknownBracketsInText(t *testing.T) {
	t.Parallel()

	f := memory.Parse("# Memory\n\n## Notes\n\n- uses slice[0] pattern [tags: go]\n")
	if len(f.Notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(f.Notes))
	}
	n := f.Notes[0]
	if n.Text != "uses slice[0] pattern" {
		t.Errorf("text = %q, want unknown brackets preserved and tags stripped", n.Text)
	}
	if diff := cmp.Diff([]string{"go"}, n.Tags); diff != "" {
		t.Errorf("tags mismatch (-want +got):\n%s", diff)
	}
}

func TestParseAcceptsFrenchFactsHeading(t *testing.T) {
	t.Parallel()

	f := memory.Parse("## Faits\n\n- langue: français\n")
	if len(f.Facts) != 1 || f.Facts[0].Key != "langue" || f.Facts[0].Value != "français" {
		t.Errorf("Faits heading not parsed as facts: %+v", f)
	}
}

func TestSetFactSupersedesByKey(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	if err := s.SetFact(memory.Fact{Key: "editor", Value: "vim"}); err != nil {
		t.Fatalf("SetFact: %v", err)
	}
	if err := s.SetFact(memory.Fact{Key: "shell", Value: "zsh"}); err != nil {
		t.Fatalf("SetFact: %v", err)
	}
	if err := s.SetFact(memory.Fact{Key: "editor", Value: "neovim", Pinned: true}); err != nil {
		t.Fatalf("SetFact supersede: %v", err)
	}

	f, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []memory.Fact{
		{Key: "editor", Value: "neovim", Pinned: true}, // superseded in place
		{Key: "shell", Value: "zsh"},
	}
	if diff := cmp.Diff(want, f.Facts); diff != "" {
		t.Errorf("facts mismatch (-want +got):\n%s", diff)
	}
}

func TestSetFactValidation(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	cases := []memory.Fact{
		{Key: "", Value: "v"},
		{Key: "k", Value: ""},
		{Key: "bad:key", Value: "v"},
		{Key: "  ", Value: "v"},
	}
	for _, f := range cases {
		if err := s.SetFact(f); err == nil {
			t.Errorf("SetFact(%+v) = nil, want error", f)
		}
	}
}

func TestAddNoteRejectsEmptyAndDedupes(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	if err := s.AddNote(memory.Note{Text: "   "}); err == nil {
		t.Error("AddNote(blank) = nil, want error")
	}
	if err := s.AddNote(memory.Note{Text: "same"}); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if err := s.AddNote(memory.Note{Text: "same"}); err != nil {
		t.Fatalf("AddNote duplicate: %v", err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Notes) != 1 {
		t.Errorf("duplicate note stored: %d notes, want 1", len(f.Notes))
	}
}

func TestPerTurnWriteCaps(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	for i := 0; i < memory.MaxFactWritesPerTurn; i++ {
		if err := s.SetFact(memory.Fact{Key: "k" + string(rune('a'+i)), Value: "v"}); err != nil {
			t.Fatalf("SetFact %d: %v", i, err)
		}
	}
	if err := s.SetFact(memory.Fact{Key: "over", Value: "v"}); !errors.Is(err, memory.ErrTurnCap) {
		t.Errorf("fact over cap: err = %v, want ErrTurnCap", err)
	}

	for i := 0; i < memory.MaxNoteWritesPerTurn; i++ {
		if err := s.AddNote(memory.Note{Text: "note " + string(rune('a'+i))}); err != nil {
			t.Fatalf("AddNote %d: %v", i, err)
		}
	}
	if err := s.AddNote(memory.Note{Text: "over"}); !errors.Is(err, memory.ErrTurnCap) {
		t.Errorf("note over cap: err = %v, want ErrTurnCap", err)
	}

	// A new turn resets both budgets.
	s.ResetTurn()
	if err := s.SetFact(memory.Fact{Key: "over", Value: "v"}); err != nil {
		t.Errorf("SetFact after ResetTurn: %v", err)
	}
	if err := s.AddNote(memory.Note{Text: "over"}); err != nil {
		t.Errorf("AddNote after ResetTurn: %v", err)
	}
}

func TestEvictionPrefersLowUtilityNotes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "MEMORY.md")
	s := memory.NewStoreWithCap(path, 170)

	// Seed a file with a pinned note, a much-used note, and a stale one.
	seed := memory.File{
		Notes: []memory.Note{
			{Text: "stale unused note", Uses: 0},
			{Text: "precious pinned note", Pinned: true},
			{Text: "frequently recalled note", Uses: 5},
		},
	}
	if err := os.WriteFile(path, []byte(seed.Marshal()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Adding one more note pushes the file over the cap; the stale unused
	// note must go first — not the oldest line (which the pinned note is not,
	// but the old byte-eviction would have dropped "stale" too; the point is
	// the used and pinned notes both survive).
	s.ResetTurn()
	if err := s.AddNote(memory.Note{Text: strings.Repeat("fresh note text ", 4)}); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	texts := make([]string, len(f.Notes))
	for i, n := range f.Notes {
		texts[i] = n.Text
	}
	joined := strings.Join(texts, "|")
	if strings.Contains(joined, "stale unused note") {
		t.Errorf("stale unused note survived eviction: %q", joined)
	}
	if !strings.Contains(joined, "precious pinned note") {
		t.Errorf("pinned note evicted before unpinned ones: %q", joined)
	}
	if !strings.Contains(joined, "frequently recalled note") {
		t.Errorf("high-utility note evicted before low-utility one: %q", joined)
	}
}

func TestSingleOversizeEntryKeptWhole(t *testing.T) {
	t.Parallel()

	s := memory.NewStoreWithCap(filepath.Join(t.TempDir(), "MEMORY.md"), 40)
	big := "this-single-note-is-way-over-the-byte-cap-on-its-own"
	if err := s.AddNote(memory.Note{Text: big}); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Notes) != 1 || f.Notes[0].Text != big {
		t.Errorf("oversize single entry should survive whole, got %+v", f.Notes)
	}
}

func TestRecordUsesPersists(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	if err := s.AddNote(memory.Note{Text: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNote(memory.Note{Text: "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUses([]string{"beta", "absent"}); err != nil {
		t.Fatalf("RecordUses: %v", err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range f.Notes {
		want := 0
		if n.Text == "beta" {
			want = 1
		}
		if n.Uses != want {
			t.Errorf("note %q uses = %d, want %d", n.Text, n.Uses, want)
		}
	}
}

func TestReadMissingFile(t *testing.T) {
	t.Parallel()

	s := memory.NewStore(filepath.Join(t.TempDir(), "absent.md"))
	got, err := s.Read()
	if err != nil {
		t.Fatalf("Read missing file returned error: %v", err)
	}
	if got != "" {
		t.Errorf("Read missing file = %q, want empty", got)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatalf("Load missing file returned error: %v", err)
	}
	if !f.Empty() {
		t.Errorf("Load missing file = %+v, want empty", f)
	}
}

func TestWriteCreatesParentDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "deeper", "MEMORY.md")
	s := memory.NewStore(path)
	if err := s.AddNote(memory.Note{Text: "hello"}); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file created at %s: %v", path, err)
	}
}
