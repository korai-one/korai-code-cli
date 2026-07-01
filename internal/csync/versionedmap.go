package csync

import "sync/atomic"

// VersionedMap is a concurrency-safe Map that maintains a monotonically
// increasing version counter. The counter is incremented on every mutation
// (Set, Delete, and a successful Take) and left untouched by reads. Callers use
// Version as a cheap "has anything changed since I last looked" primitive — for
// example a diagnostics store that wants to know when a language server has
// stopped emitting updates and the map has settled. The zero value is not
// usable; construct one with NewVersionedMap.
type VersionedMap[K comparable, V any] struct {
	m *Map[K, V]
	v atomic.Uint64
}

// NewVersionedMap creates a new, empty VersionedMap with version 0.
func NewVersionedMap[K comparable, V any]() *VersionedMap[K, V] {
	return &VersionedMap[K, V]{m: NewMap[K, V]()}
}

// Get returns the value stored for k and whether it was present. It does not
// change the version.
func (m *VersionedMap[K, V]) Get(k K) (V, bool) {
	return m.m.Get(k)
}

// Set stores v under k and increments the version.
func (m *VersionedMap[K, V]) Set(k K, v V) {
	m.m.Set(k, v)
	m.v.Add(1)
}

// Delete removes k from the map and increments the version. The version is
// bumped even if k was absent, since callers treat any Delete as a potential
// change.
func (m *VersionedMap[K, V]) Delete(k K) {
	m.m.Delete(k)
	m.v.Add(1)
}

// Len returns the number of entries in the map. It does not change the version.
func (m *VersionedMap[K, V]) Len() int {
	return m.m.Len()
}

// Take atomically returns and removes the value stored for k. When k was
// present (the returned boolean is true) the version is incremented, since the
// map changed; an unsuccessful Take leaves the version untouched.
func (m *VersionedMap[K, V]) Take(k K) (V, bool) {
	v, ok := m.m.Take(k)
	if ok {
		m.v.Add(1)
	}
	return v, ok
}

// Range calls fn for each key/value pair in a snapshot of the map, stopping
// early if fn returns false. See Map.Range for the snapshot semantics. It does
// not change the version.
func (m *VersionedMap[K, V]) Range(fn func(k K, v V) bool) {
	m.m.Range(fn)
}

// Version returns the current version counter with a lock-free atomic load.
func (m *VersionedMap[K, V]) Version() uint64 {
	return m.v.Load()
}
