package engine

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestStripVolatile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "uuid",
			in:   "request 8f14e45f-ceea-4a7b-9c3d-1a2b3c4d5e6f done",
			want: "request <uuid> done",
		},
		{
			name: "iso timestamp with fraction and zone",
			in:   "at 2026-07-23T14:03:59.123Z it ran",
			want: "at <ts> it ran",
		},
		{
			name: "timestamp with space and offset",
			in:   "logged 2026-07-23 14:03:59+02:00 ok",
			want: "logged <ts> ok",
		},
		{
			name: "long digit run",
			in:   "pid 1234567 exited",
			want: "pid <n> exited",
		},
		{
			name: "short numbers preserved",
			in:   "line 42 of file.go in 2026",
			want: "line 42 of file.go in 2026",
		},
		{
			name: "mixed",
			in:   "id=8f14e45f-ceea-4a7b-9c3d-1a2b3c4d5e6f ts=2026-07-23T14:03:59Z epoch=1753272239",
			want: "id=<uuid> ts=<ts> epoch=<n>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stripVolatile(tt.in); got != tt.want {
				t.Errorf("stripVolatile(%q) mismatch (-want +got):\n%s", tt.in, cmp.Diff(tt.want, got))
			}
		})
	}
}

func TestNormalizeArgs(t *testing.T) {
	t.Parallel()

	a := normalizeArgs(json.RawMessage(`{"a":1,"b":"x"}`))
	b := normalizeArgs(json.RawMessage("{ \"b\": \"x\",\n  \"a\": 1 }"))
	if a != b {
		t.Error("key order / whitespace should not change the normalized hash")
	}
	c := normalizeArgs(json.RawMessage(`{"a":2,"b":"x"}`))
	if a == c {
		t.Error("different argument values must hash differently")
	}
	// Unparsable input falls back to raw bytes: still deterministic, still
	// distinct from other garbage.
	g1 := normalizeArgs(json.RawMessage(`{"broken`))
	g2 := normalizeArgs(json.RawMessage(`  {"broken  `))
	if g1 != g2 {
		t.Error("trimmed identical garbage should hash identically")
	}
	if g1 == normalizeArgs(json.RawMessage(`{"other`)) {
		t.Error("different garbage must hash differently")
	}
}

// TestLoopDetectorEscalation walks the veto state machine: silent first call,
// warn on the second identical no-progress call, veto from the third on.
func TestLoopDetectorEscalation(t *testing.T) {
	t.Parallel()

	d := newLoopDetector()
	args := json.RawMessage(`{"path":"a.go"}`)

	if d.check("Read", args) {
		t.Fatal("first call must not be vetoed")
	}
	if d.observe("Read", args, "contents") {
		t.Error("first observation must not warn")
	}

	if d.check("Read", args) {
		t.Fatal("second call must not be vetoed")
	}
	if !d.observe("Read", args, "contents") {
		t.Error("second identical no-progress call must warn")
	}

	if !d.check("Read", args) {
		t.Error("third identical no-progress call must be vetoed")
	}
	if !d.check("Read", args) {
		t.Error("fourth identical call must stay vetoed")
	}
	if d.vetoCount() != 2 {
		t.Errorf("vetoCount = %d, want 2", d.vetoCount())
	}

	// A different tool or different args is unaffected.
	if d.check("Read", json.RawMessage(`{"path":"b.go"}`)) {
		t.Error("different args must not be vetoed")
	}
	if d.check("Grep", args) {
		t.Error("different tool must not be vetoed")
	}
}

// TestLoopDetectorProgressResets verifies that an identical call whose result
// changed counts as progress: the streak resets and no warn/veto fires.
func TestLoopDetectorProgressResets(t *testing.T) {
	t.Parallel()

	d := newLoopDetector()
	args := json.RawMessage(`{}`)

	for i, result := range []string{"one", "two", "three", "four"} {
		if d.check("Bash", args) {
			t.Fatalf("call %d: vetoed despite changing results", i+1)
		}
		if d.observe("Bash", args, result) {
			t.Errorf("call %d: warned despite changing results", i+1)
		}
	}
	if d.vetoCount() != 0 {
		t.Errorf("vetoCount = %d, want 0", d.vetoCount())
	}
}

// TestLoopDetectorVolatileResultStillNoProgress verifies that results differing
// only in volatile content (timestamps, ids) still count as a no-progress
// streak.
func TestLoopDetectorVolatileResultStillNoProgress(t *testing.T) {
	t.Parallel()

	d := newLoopDetector()
	args := json.RawMessage(`{"q":"status"}`)

	d.check("Bash", args)
	d.observe("Bash", args, "checked at 2026-07-23T10:00:00Z: nothing new")
	d.check("Bash", args)
	if !d.observe("Bash", args, "checked at 2026-07-23T10:00:07Z: nothing new") {
		t.Error("timestamp-only difference must still count as no progress (warn)")
	}
	if !d.check("Bash", args) {
		t.Error("third volatile-only repeat must be vetoed")
	}
}
