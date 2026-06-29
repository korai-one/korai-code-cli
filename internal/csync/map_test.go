package csync

import (
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMapGetSetDeleteLen(t *testing.T) {
	m := NewMap[string, int]()

	if got := m.Len(); got != 0 {
		t.Fatalf("Len of empty map = %d, want 0", got)
	}
	if _, ok := m.Get("missing"); ok {
		t.Fatalf("Get(missing) reported present on empty map")
	}

	m.Set("a", 1)
	m.Set("b", 2)
	if got := m.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}

	v, ok := m.Get("a")
	if !ok || v != 1 {
		t.Fatalf("Get(a) = (%d, %t), want (1, true)", v, ok)
	}

	// Overwrite.
	m.Set("a", 10)
	if v, _ := m.Get("a"); v != 10 {
		t.Fatalf("Get(a) after overwrite = %d, want 10", v)
	}

	m.Delete("a")
	if _, ok := m.Get("a"); ok {
		t.Fatalf("Get(a) reported present after Delete")
	}
	if got := m.Len(); got != 1 {
		t.Fatalf("Len after Delete = %d, want 1", got)
	}

	// Deleting an absent key is a no-op.
	m.Delete("does-not-exist")
	if got := m.Len(); got != 1 {
		t.Fatalf("Len after no-op Delete = %d, want 1", got)
	}
}

func TestMapGetOrSet(t *testing.T) {
	m := NewMap[string, int]()

	v, loaded := m.GetOrSet("x", 1)
	if loaded || v != 1 {
		t.Fatalf("GetOrSet(x,1) on empty = (%d, %t), want (1, false)", v, loaded)
	}

	v, loaded = m.GetOrSet("x", 99)
	if !loaded || v != 1 {
		t.Fatalf("GetOrSet(x,99) when present = (%d, %t), want (1, true)", v, loaded)
	}

	// Confirm the stored value did not change.
	if got, _ := m.Get("x"); got != 1 {
		t.Fatalf("Get(x) = %d, want 1 (GetOrSet must not overwrite)", got)
	}
}

func TestMapTake(t *testing.T) {
	m := NewMap[string, int]()
	m.Set("a", 1)

	v, ok := m.Take("a")
	if !ok || v != 1 {
		t.Fatalf("Take(a) = (%d, %t), want (1, true)", v, ok)
	}
	if _, ok := m.Get("a"); ok {
		t.Fatalf("Take did not remove the key")
	}

	v, ok = m.Take("a")
	if ok || v != 0 {
		t.Fatalf("Take(a) when absent = (%d, %t), want (0, false)", v, ok)
	}
}

func TestMapRange(t *testing.T) {
	m := NewMap[string, int]()
	want := map[string]int{"a": 1, "b": 2, "c": 3}
	for k, v := range want {
		m.Set(k, v)
	}

	got := make(map[string]int)
	m.Range(func(k string, v int) bool {
		got[k] = v
		return true
	})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Range visited entries mismatch (-want +got):\n%s", diff)
	}
}

func TestMapRangeEarlyStop(t *testing.T) {
	m := NewMap[int, int]()
	for i := 0; i < 100; i++ {
		m.Set(i, i)
	}

	var visited int
	m.Range(func(k, v int) bool {
		visited++
		return visited < 3 // stop after the third call
	})
	if visited != 3 {
		t.Fatalf("Range visited %d entries, want 3 (early stop)", visited)
	}
}

func TestMapRangeSnapshotAllowsReentrancy(t *testing.T) {
	// Range snapshots under a read lock and iterates without holding it, so fn
	// may safely call back into the map without deadlocking.
	m := NewMap[int, int]()
	for i := 0; i < 5; i++ {
		m.Set(i, i)
	}

	var keys []int
	m.Range(func(k, v int) bool {
		keys = append(keys, k)
		m.Set(k+1000, v) // mutate during iteration; must not deadlock
		return true
	})

	sort.Ints(keys)
	if diff := cmp.Diff([]int{0, 1, 2, 3, 4}, keys); diff != "" {
		t.Fatalf("Range over snapshot mismatch (-want +got):\n%s", diff)
	}
}

func TestMapConcurrentAccess(t *testing.T) {
	m := NewMap[int, int]()
	const goroutines = 50
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				k := base*iterations + i
				m.Set(k, k)
				m.Get(k)
				m.GetOrSet(k, k)
				m.Len()
				m.Range(func(int, int) bool { return false })
				m.Delete(k)
				m.Take(k)
			}
		}(g)
	}
	wg.Wait()

	if got := m.Len(); got != 0 {
		t.Fatalf("Len after all keys deleted = %d, want 0", got)
	}
}
