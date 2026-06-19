package tui

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// step describes a single navigation action and the result it must produce.
type step struct {
	dir     string // "prev" or "next"
	wantStr string
	wantOK  bool
}

func TestInputHistoryAdd(t *testing.T) {
	tests := []struct {
		name string
		adds []string
		want []string
	}{
		{
			name: "empty start",
			adds: nil,
			want: nil,
		},
		{
			name: "single entry",
			adds: []string{"one"},
			want: []string{"one"},
		},
		{
			name: "multiple entries preserve order",
			adds: []string{"one", "two", "three"},
			want: []string{"one", "two", "three"},
		},
		{
			name: "blank entries ignored",
			adds: []string{"", "one", "", "two"},
			want: []string{"one", "two"},
		},
		{
			name: "adjacent duplicates deduped",
			adds: []string{"one", "one", "two", "two", "one"},
			want: []string{"one", "two", "one"},
		},
		{
			name: "non-adjacent duplicates kept",
			adds: []string{"one", "two", "one"},
			want: []string{"one", "two", "one"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h inputHistory
			for _, s := range tt.adds {
				h.add(s)
			}
			if diff := cmp.Diff(tt.want, h.entries); diff != "" {
				t.Errorf("entries mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestInputHistoryNavigate(t *testing.T) {
	tests := []struct {
		name  string
		adds  []string
		steps []step
	}{
		{
			name: "empty history yields nothing",
			adds: nil,
			steps: []step{
				{dir: "prev", wantStr: "", wantOK: false},
				{dir: "next", wantStr: "", wantOK: false},
			},
		},
		{
			name: "single entry prev clamps at oldest",
			adds: []string{"one"},
			steps: []step{
				{dir: "prev", wantStr: "one", wantOK: true},
				{dir: "prev", wantStr: "one", wantOK: true},
			},
		},
		{
			name: "single entry next returns to draft then stops",
			adds: []string{"one"},
			steps: []step{
				{dir: "prev", wantStr: "one", wantOK: true},
				{dir: "next", wantStr: "", wantOK: true},
				{dir: "next", wantStr: "", wantOK: false},
			},
		},
		{
			name: "prev starts at newest and walks back to oldest, clamping",
			adds: []string{"one", "two", "three"},
			steps: []step{
				{dir: "prev", wantStr: "three", wantOK: true},
				{dir: "prev", wantStr: "two", wantOK: true},
				{dir: "prev", wantStr: "one", wantOK: true},
				{dir: "prev", wantStr: "one", wantOK: true},
			},
		},
		{
			name: "next walks toward newest then lands on empty draft",
			adds: []string{"one", "two", "three"},
			steps: []step{
				{dir: "prev", wantStr: "three", wantOK: true},
				{dir: "prev", wantStr: "two", wantOK: true},
				{dir: "prev", wantStr: "one", wantOK: true},
				{dir: "next", wantStr: "two", wantOK: true},
				{dir: "next", wantStr: "three", wantOK: true},
				{dir: "next", wantStr: "", wantOK: true},
				{dir: "next", wantStr: "", wantOK: false},
			},
		},
		{
			name: "next before any prev is not navigating",
			adds: []string{"one", "two"},
			steps: []step{
				{dir: "next", wantStr: "", wantOK: false},
				{dir: "prev", wantStr: "two", wantOK: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h inputHistory
			for _, s := range tt.adds {
				h.add(s)
			}
			for i, st := range tt.steps {
				var gotStr string
				var gotOK bool
				switch st.dir {
				case "prev":
					gotStr, gotOK = h.prev()
				case "next":
					gotStr, gotOK = h.next()
				default:
					t.Fatalf("step %d: unknown dir %q", i, st.dir)
				}
				if gotStr != st.wantStr || gotOK != st.wantOK {
					t.Errorf("step %d (%s): got (%q, %t), want (%q, %t)",
						i, st.dir, gotStr, gotOK, st.wantStr, st.wantOK)
				}
			}
		})
	}
}

func TestInputHistoryAddResetsNavigation(t *testing.T) {
	var h inputHistory
	h.add("one")
	h.add("two")

	// Begin navigating backward.
	if got, ok := h.prev(); got != "two" || !ok {
		t.Fatalf("prev: got (%q, %t), want (two, true)", got, ok)
	}
	if got, ok := h.prev(); got != "one" || !ok {
		t.Fatalf("prev: got (%q, %t), want (one, true)", got, ok)
	}

	// Adding a new entry must restart navigation from the newest.
	h.add("three")
	if got, ok := h.prev(); got != "three" || !ok {
		t.Errorf("after add, prev: got (%q, %t), want (three, true)", got, ok)
	}
}

func TestInputHistoryResetRestartsNavigation(t *testing.T) {
	var h inputHistory
	h.add("one")
	h.add("two")

	if got, ok := h.prev(); got != "two" || !ok {
		t.Fatalf("prev: got (%q, %t), want (two, true)", got, ok)
	}
	h.reset()
	if got, ok := h.prev(); got != "two" || !ok {
		t.Errorf("after reset, prev: got (%q, %t), want (two, true)", got, ok)
	}
}
