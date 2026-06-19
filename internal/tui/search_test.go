package tui

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTranscriptSearchRun(t *testing.T) {
	texts := []string{
		"the quick brown fox",
		"jumps over the lazy dog",
		"THE END",
		"nothing here",
	}

	tests := []struct {
		name     string
		query    string
		wantHits []int
		wantCur  int
		wantOK   bool
	}{
		{
			name:     "no matches",
			query:    "zzz",
			wantHits: nil,
			wantOK:   false,
		},
		{
			name:     "single match",
			query:    "fox",
			wantHits: []int{0},
			wantCur:  0,
			wantOK:   true,
		},
		{
			name:     "multiple matches with index mapping",
			query:    "the",
			wantHits: []int{0, 1, 2},
			wantCur:  0,
			wantOK:   true,
		},
		{
			name:     "case insensitive query",
			query:    "THE",
			wantHits: []int{0, 1, 2},
			wantCur:  0,
			wantOK:   true,
		},
		{
			name:     "case insensitive text",
			query:    "end",
			wantHits: []int{2},
			wantCur:  2,
			wantOK:   true,
		},
		{
			name:     "blank query clears",
			query:    "",
			wantHits: nil,
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s transcriptSearch
			s.run(tt.query, texts)

			if got := s.query(); got != tt.query {
				t.Errorf("query() = %q, want %q", got, tt.query)
			}
			if diff := cmp.Diff(tt.wantHits, s.hits()); diff != "" {
				t.Errorf("hits() mismatch (-want +got):\n%s", diff)
			}
			gotCur, gotOK := s.current()
			if gotOK != tt.wantOK {
				t.Errorf("current() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotOK && gotCur != tt.wantCur {
				t.Errorf("current() = %d, want %d", gotCur, tt.wantCur)
			}
		})
	}
}

func TestTranscriptSearchNavigation(t *testing.T) {
	texts := []string{"a match", "b match", "c match"}

	t.Run("nextHit wraps forward", func(t *testing.T) {
		var s transcriptSearch
		s.run("match", texts)

		want := []int{0, 1, 2, 0}
		var got []int
		if cur, ok := s.current(); ok {
			got = append(got, cur)
		}
		for i := 0; i < 3; i++ {
			s.nextHit()
			cur, ok := s.current()
			if !ok {
				t.Fatal("current() not ok during navigation")
			}
			got = append(got, cur)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("forward navigation mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prevHit wraps backward", func(t *testing.T) {
		var s transcriptSearch
		s.run("match", texts)

		want := []int{0, 2, 1, 0}
		var got []int
		if cur, ok := s.current(); ok {
			got = append(got, cur)
		}
		for i := 0; i < 3; i++ {
			s.prevHit()
			cur, ok := s.current()
			if !ok {
				t.Fatal("current() not ok during navigation")
			}
			got = append(got, cur)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("backward navigation mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("nextHit and prevHit no-op when empty", func(t *testing.T) {
		var s transcriptSearch
		s.run("nope", texts)
		s.nextHit()
		s.prevHit()
		if _, ok := s.current(); ok {
			t.Error("current() ok = true, want false on empty search")
		}
	})
}

func TestTranscriptSearchClear(t *testing.T) {
	texts := []string{"alpha", "beta", "alpha beta"}

	var s transcriptSearch
	s.run("alpha", texts)
	s.nextHit()
	s.clear()

	if got := s.query(); got != "" {
		t.Errorf("query() after clear = %q, want empty", got)
	}
	if diff := cmp.Diff([]int(nil), s.hits()); diff != "" {
		t.Errorf("hits() after clear mismatch (-want +got):\n%s", diff)
	}
	if _, ok := s.current(); ok {
		t.Error("current() ok = true after clear, want false")
	}
}

func TestTranscriptSearchRerun(t *testing.T) {
	texts := []string{"red", "green", "blue", "green again"}

	var s transcriptSearch
	s.run("green", texts)
	if diff := cmp.Diff([]int{1, 3}, s.hits()); diff != "" {
		t.Fatalf("first run hits mismatch (-want +got):\n%s", diff)
	}
	s.nextHit() // move off the first match

	// Re-run with a new query recomputes from scratch and resets current.
	s.run("blue", texts)
	if diff := cmp.Diff([]int{2}, s.hits()); diff != "" {
		t.Errorf("second run hits mismatch (-want +got):\n%s", diff)
	}
	cur, ok := s.current()
	if !ok || cur != 2 {
		t.Errorf("current() = (%d, %v), want (2, true)", cur, ok)
	}
}
