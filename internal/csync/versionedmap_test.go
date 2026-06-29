package csync

import (
	"sync"
	"testing"
)

func TestVersionedMapVersionBumps(t *testing.T) {
	m := NewVersionedMap[string, int]()
	if got := m.Version(); got != 0 {
		t.Fatalf("initial Version = %d, want 0", got)
	}

	m.Set("a", 1)
	if got := m.Version(); got != 1 {
		t.Fatalf("Version after Set = %d, want 1", got)
	}

	m.Set("a", 2)
	if got := m.Version(); got != 2 {
		t.Fatalf("Version after second Set = %d, want 2", got)
	}

	m.Delete("a")
	if got := m.Version(); got != 3 {
		t.Fatalf("Version after Delete = %d, want 3", got)
	}

	// Delete of an absent key still bumps (any Delete is a potential change).
	m.Delete("absent")
	if got := m.Version(); got != 4 {
		t.Fatalf("Version after no-op Delete = %d, want 4", got)
	}
}

func TestVersionedMapVersionStableOnReads(t *testing.T) {
	m := NewVersionedMap[string, int]()
	m.Set("a", 1)
	base := m.Version()

	m.Get("a")
	m.Get("missing")
	m.Len()
	m.Range(func(string, int) bool { return true })

	if got := m.Version(); got != base {
		t.Fatalf("Version changed by reads: got %d, want %d", got, base)
	}
}

func TestVersionedMapTakeBumpsOnlyWhenPresent(t *testing.T) {
	m := NewVersionedMap[string, int]()
	m.Set("a", 1)
	base := m.Version()

	if v, ok := m.Take("a"); !ok || v != 1 {
		t.Fatalf("Take(a) = (%d, %t), want (1, true)", v, ok)
	}
	if got := m.Version(); got != base+1 {
		t.Fatalf("Version after successful Take = %d, want %d", got, base+1)
	}

	after := m.Version()
	if _, ok := m.Take("a"); ok {
		t.Fatalf("Take(a) reported present after removal")
	}
	if got := m.Version(); got != after {
		t.Fatalf("Version changed by unsuccessful Take: got %d, want %d", got, after)
	}
}

func TestVersionedMapStorageBehavior(t *testing.T) {
	m := NewVersionedMap[string, int]()
	m.Set("a", 1)
	m.Set("b", 2)

	if got := m.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	if v, ok := m.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = (%d, %t), want (1, true)", v, ok)
	}

	seen := make(map[string]int)
	m.Range(func(k string, v int) bool {
		seen[k] = v
		return true
	})
	if len(seen) != 2 || seen["a"] != 1 || seen["b"] != 2 {
		t.Fatalf("Range saw %v, want a=1 b=2", seen)
	}
}

func TestVersionedMapConcurrentAccess(t *testing.T) {
	m := NewVersionedMap[int, int]()
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
				m.Version()
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
	// Every Set + Delete bumps; exact total is deterministic regardless of
	// interleaving: goroutines*iterations Sets + the same number of Deletes.
	// Takes never succeed here (Delete already removed the key), so they add 0.
	wantVersion := uint64(2 * goroutines * iterations)
	if got := m.Version(); got != wantVersion {
		t.Fatalf("final Version = %d, want %d", got, wantVersion)
	}
}
