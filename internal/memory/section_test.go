package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Nevaero/korai-code-cli/internal/memory"
)

func TestRecallRanksOverlapTagsAndRecency(t *testing.T) {
	t.Parallel()

	f := memory.File{Notes: []memory.Note{
		{Text: "the deploy pipeline uses fly.io and needs the fly CLI"},
		{Text: "unrelated note about croissants"},
		{Text: "tests must be table-driven", Tags: []string{"deploy"}},
		{Text: "deploy secrets live in the vault"},
	}}

	section, recalled := memory.RenderSection(f, "how do I deploy this?")
	if len(recalled) == 0 {
		t.Fatal("expected recalled notes for a matching query")
	}
	// Every recalled note mentions the query topic; the unrelated one never
	// surfaces in full.
	for _, n := range recalled {
		if strings.Contains(n.Text, "croissants") {
			t.Errorf("irrelevant note recalled: %q", n.Text)
		}
	}
	if !strings.Contains(section, "## Rappels") {
		t.Errorf("section missing Rappels heading:\n%s", section)
	}
	if strings.Contains(section, "croissants") {
		t.Errorf("irrelevant note injected:\n%s", section)
	}
}

func TestRecallFrenchStopwordsAndAccents(t *testing.T) {
	t.Parallel()

	f := memory.File{Notes: []memory.Note{
		{Text: "le générateur préfère les modèles quantisés"},
		{Text: "notes about something else entirely"},
	}}
	_, recalled := memory.RenderSection(f, "est-ce que le générateur est prêt ?")
	if len(recalled) != 1 || !strings.Contains(recalled[0].Text, "générateur") {
		t.Errorf("accented term overlap not recalled: %+v", recalled)
	}

	// A query of only stopwords recalls nothing.
	_, recalled = memory.RenderSection(f, "est-ce que le la les de et")
	if len(recalled) != 0 {
		t.Errorf("stopword-only query recalled %+v, want none", recalled)
	}
}

func TestPinnedAndGatedFacts(t *testing.T) {
	t.Parallel()

	f := memory.File{Facts: []memory.Fact{
		{Key: "editor", Value: "neovim", Pinned: true},
		{Key: "plain", Value: "always here"}, // no keywords → always injected
		{Key: "deploy-target", Value: "fly.io", Keywords: []string{"deploy"}},
	}}

	section, _ := memory.RenderSection(f, "tell me about go generics")
	if !strings.Contains(section, "editor: neovim") || !strings.Contains(section, "plain: always here") {
		t.Errorf("always-injected facts missing:\n%s", section)
	}
	if strings.Contains(section, "deploy-target") {
		t.Errorf("keyword-gated fact injected without a match:\n%s", section)
	}

	section, _ = memory.RenderSection(f, "how do I deploy?")
	if !strings.Contains(section, "deploy-target: fly.io") {
		t.Errorf("keyword-gated fact missing on match:\n%s", section)
	}
	// Substring-only occurrences ("deployment") do not open the gate.
	section, _ = memory.RenderSection(f, "read the deployment doc")
	if strings.Contains(section, "deploy-target") {
		t.Errorf("keyword gate opened on a partial word:\n%s", section)
	}
}

func TestPinnedNotesAlwaysInjected(t *testing.T) {
	t.Parallel()

	f := memory.File{Notes: []memory.Note{
		{Text: "legacy line from the flat file", Pinned: true},
		{Text: "recall-gated note about parsers"},
	}}
	section, _ := memory.RenderSection(f, "completely unrelated question")
	if !strings.Contains(section, "legacy line from the flat file") {
		t.Errorf("pinned note missing from section:\n%s", section)
	}
	if strings.Contains(section, "parsers") {
		t.Errorf("unpinned note injected without recall match:\n%s", section)
	}
}

func TestSectionBudgetsClipWholeEntries(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 600)
	var f memory.File
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		f.Facts = append(f.Facts, memory.Fact{Key: k, Value: long, Pinned: true})
	}
	section, _ := memory.RenderSection(f, "")
	factsPart := section[strings.Index(section, "## Faits"):]
	if len(factsPart) > memory.FactsBudgetChars+len("## Faits\n")+16 {
		t.Errorf("facts section exceeds budget: %d chars", len(factsPart))
	}
	// Clipping drops whole facts from the tail: "a" stays, "f" goes.
	if !strings.Contains(section, "- a: ") {
		t.Error("first fact clipped away")
	}
	if strings.Contains(section, "- f: ") {
		t.Error("facts beyond the budget were not clipped")
	}
}

func TestRecallTopKAndPointers(t *testing.T) {
	t.Parallel()

	var f memory.File
	for i := 0; i < memory.RecallTopK+memory.RecallPointerMax+2; i++ {
		f.Notes = append(f.Notes, memory.Note{
			Text: "compaction detail " + strings.Repeat("v", i+1),
		})
	}
	section, recalled := memory.RenderSection(f, "explain the compaction detail")
	if len(recalled) != memory.RecallTopK {
		t.Fatalf("recalled %d notes in full, want %d", len(recalled), memory.RecallTopK)
	}
	if !strings.Contains(section, "autres notes") {
		t.Errorf("pointer line missing:\n%s", section)
	}
}

func TestEmptyFileRendersNothing(t *testing.T) {
	t.Parallel()

	section, recalled := memory.RenderSection(memory.File{}, "anything")
	if section != "" || recalled != nil {
		t.Errorf("empty file rendered %q / %+v, want nothing", section, recalled)
	}
}

func TestProviderReflectsMidSessionWritesAndRecordsUses(t *testing.T) {
	t.Parallel()

	store := memory.NewStore(filepath.Join(t.TempDir(), "MEMORY.md"))
	p := memory.NewProvider(store)

	if got := p.Section("bootstrap question"); got != "" {
		t.Fatalf("empty store rendered %q, want empty", got)
	}

	// A write becomes visible on the very next call — no restart needed.
	if err := store.AddNote(memory.Note{Text: "the API uses fence dialect", Tags: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	got := p.Section("how does the api fence work?")
	if !strings.Contains(got, "fence dialect") {
		t.Errorf("mid-session write invisible to provider:\n%s", got)
	}

	// The recall was recorded once for the query, not once per request build.
	_ = p.Section("how does the api fence work?")
	f, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Notes[0].Uses != 1 {
		t.Errorf("uses = %d after two renders of one query, want 1", f.Notes[0].Uses)
	}
}

// TestSectionGolden pins the full rendered section shape (headings, ordering,
// pointer line) against a golden file. Regenerate with -update.
func TestSectionGolden(t *testing.T) {
	t.Parallel()

	f := memory.File{
		Facts: []memory.Fact{
			{Key: "editor", Value: "neovim", Pinned: true},
			{Key: "deploy-target", Value: "fly.io", Keywords: []string{"deploy"}},
			{Key: "lang", Value: "français"},
		},
		Notes: []memory.Note{
			{Text: "legacy always-on note", Pinned: true},
			{Text: "deploy needs the fly CLI installed", Tags: []string{"deploy"}},
			{Text: "deploy secrets come from the vault"},
			{Text: "deploy dashboards live in grafana"},
			{Text: "deploy rollbacks use fly releases and this pointer line is deliberately long enough to be clipped in the preview rendering", Tags: []string{"ops"}},
			{Text: "unrelated trivia about croissants"},
		},
	}
	section, _ := memory.RenderSection(f, "how do I deploy this service?")

	goldenPath := filepath.Join("..", "..", "testdata", "golden", "memory_section.txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(section), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden file missing — run with -update to create it: %v", err)
	}
	if diff := cmp.Diff(string(want), section); diff != "" {
		t.Errorf("section mismatch (-want +got):\n%s", diff)
	}
}
