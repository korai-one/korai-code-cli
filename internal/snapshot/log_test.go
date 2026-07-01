package snapshot

import (
	"strings"
	"testing"
)

// TestLogAddAtTruncate exercises the ordered history: At counts from the end,
// Truncate drops the targeted entry and everything newer, and out-of-range
// selections report not-ok rather than panicking.
func TestLogAddAtTruncate(t *testing.T) {
	var l Log
	if l.Len() != 0 {
		t.Fatalf("fresh log Len = %d, want 0", l.Len())
	}
	if _, ok := l.At(1); ok {
		t.Fatal("At(1) on empty log should be not-ok")
	}

	l.Add("first turn", "id1")
	l.Add("second turn", "id2")
	l.Add("third turn", "id3")
	if l.Len() != 3 {
		t.Fatalf("Len = %d, want 3", l.Len())
	}

	// stepsBack=1 is the most recent.
	if e, ok := l.At(1); !ok || e.ID != "id3" || e.Label != "third turn" {
		t.Errorf("At(1) = %+v ok=%v, want id3/third turn", e, ok)
	}
	if e, ok := l.At(3); !ok || e.ID != "id1" {
		t.Errorf("At(3) = %+v ok=%v, want id1", e, ok)
	}
	if _, ok := l.At(4); ok {
		t.Error("At(4) out of range should be not-ok")
	}
	if _, ok := l.At(0); ok {
		t.Error("At(0) should be not-ok")
	}

	// Reverting two steps back drops the two newest, leaving only "first turn".
	l.Truncate(2)
	if l.Len() != 1 {
		t.Fatalf("after Truncate(2) Len = %d, want 1", l.Len())
	}
	if e, ok := l.At(1); !ok || e.ID != "id1" {
		t.Errorf("after Truncate(2) At(1) = %+v ok=%v, want id1", e, ok)
	}
}

// TestLogRender shows an empty hint and then a newest-first, step-numbered list.
func TestLogRender(t *testing.T) {
	var l Log
	if got := l.Render(); !strings.Contains(got, "No snapshots") {
		t.Errorf("empty Render = %q, want a no-snapshots hint", got)
	}

	l.Add("edit readme", "id1")
	l.Add("refactor api", "id2")
	got := l.Render()
	// Newest first: "refactor api" is step 1, "edit readme" is step 2.
	one := strings.Index(got, "1  before: refactor api")
	two := strings.Index(got, "2  before: edit readme")
	if one < 0 || two < 0 {
		t.Fatalf("Render missing expected entries:\n%s", got)
	}
	if one > two {
		t.Errorf("Render should list newest (step 1) before older (step 2):\n%s", got)
	}
}
