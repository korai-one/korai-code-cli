// Package csync provides generic, concurrency-safe containers built on the
// standard library only. The containers here are intended for moderate
// contention where a plain map guarded by an RWMutex is simpler to reason about
// than sync.Map and keeps full generic type safety.
package csync

import "sync"

// Map is a concurrency-safe generic map guarded by an RWMutex. Reads take a
// read lock so they may proceed in parallel; mutations take the write lock.
// The zero value is not usable; construct one with NewMap.
type Map[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

// NewMap creates a new, empty concurrency-safe Map.
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{m: make(map[K]V)}
}

// Get returns the value stored for k and a boolean reporting whether it was
// present. It takes a read lock.
func (m *Map[K, V]) Get(k K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.m[k]
	return v, ok
}

// Set stores v under k, overwriting any existing value. It takes the write lock.
func (m *Map[K, V]) Set(k K, v V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[k] = v
}

// Delete removes k from the map. Deleting an absent key is a no-op. It takes the
// write lock.
func (m *Map[K, V]) Delete(k K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, k)
}

// Len returns the number of entries in the map. It takes a read lock.
func (m *Map[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.m)
}

// GetOrSet returns the value already stored for k if present; otherwise it
// stores v and returns it. The returned boolean reports whether the value was
// loaded (true) rather than freshly stored (false). The check-and-set is atomic
// under the write lock, so concurrent callers agree on a single winner.
func (m *Map[K, V]) GetOrSet(k K, v V) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.m[k]; ok {
		return existing, true
	}
	m.m[k] = v
	return v, false
}

// Take atomically returns the value stored for k and removes it from the map.
// If k is absent it returns the zero value and false. The get-and-delete happens
// under a single write lock so no concurrent mutation can interleave.
func (m *Map[K, V]) Take(k K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[k]
	if ok {
		delete(m.m, k)
	}
	return v, ok
}

// Range calls fn for each key/value pair in the map, stopping early if fn
// returns false. It snapshots the entries under a read lock and then iterates
// the snapshot with no lock held. This avoids re-entrancy deadlocks (fn is free
// to call back into the map) at the cost of observing a point-in-time view:
// entries added or removed during iteration are not reflected. Iteration order
// is unspecified.
func (m *Map[K, V]) Range(fn func(k K, v V) bool) {
	m.mu.RLock()
	pairs := make([]struct {
		k K
		v V
	}, 0, len(m.m))
	for k, v := range m.m {
		pairs = append(pairs, struct {
			k K
			v V
		}{k, v})
	}
	m.mu.RUnlock()

	for _, p := range pairs {
		if !fn(p.k, p.v) {
			return
		}
	}
}
