package cache

import (
	"context"
	"sync"
	"time"
)

// DefaultMaxEntries bounds the in-memory store.
//
// A cache without a ceiling is a memory leak with good intentions: keys derived
// from user input — a search term, a URL — arrive without limit, and nothing
// ever removes them.
const DefaultMaxEntries = 10_000

type entry struct {
	value     []byte
	expiresAt time.Time
	// touched orders eviction. It is updated on read, so what gets discarded is
	// what has gone longest without being wanted.
	touched time.Time
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

// Memory is a per-process cache.
//
// It is the right default for a single binary: no infrastructure, and reads
// cost nothing. It is the wrong choice behind several instances, where each
// keeps its own copy and an invalidation on one is invisible to the others.
type Memory struct {
	// MaxEntries caps the store. Zero means DefaultMaxEntries.
	MaxEntries int

	mu      sync.Mutex
	entries map[string]entry
}

// NewMemory builds an in-memory store.
func NewMemory() *Memory {
	return &Memory{entries: make(map[string]entry)}
}

func (m *Memory) limit() int {
	if m.MaxEntries > 0 {
		return m.MaxEntries
	}
	return DefaultMaxEntries
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		return nil, false, nil
	}

	now := time.Now()
	if e.expired(now) {
		delete(m.entries, key)
		return nil, false, nil
	}

	e.touched = now
	m.entries[key] = e

	// A copy, so a caller mutating the slice cannot corrupt what the next
	// reader sees.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, true, nil
}

// Set implements Store.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	var expires time.Time
	if ttl > 0 {
		expires = now.Add(ttl)
	}

	stored := make([]byte, len(value))
	copy(stored, value)

	m.entries[key] = entry{value: stored, expiresAt: expires, touched: now}
	m.evict(now)
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.entries, key)
	return nil
}

// Clear implements Store.
func (m *Memory) Clear(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = make(map[string]entry)
	return nil
}

// Len reports how many entries are held, expired ones included.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// evict brings the store back under its limit. Callers hold the lock.
//
// Expired entries go first, since discarding those costs nothing. Only if that
// is not enough does it start on live entries, oldest-used first.
func (m *Memory) evict(now time.Time) {
	limit := m.limit()
	if len(m.entries) <= limit {
		return
	}

	for key, e := range m.entries {
		if e.expired(now) {
			delete(m.entries, key)
		}
	}
	if len(m.entries) <= limit {
		return
	}

	// Evict the least recently used, one at a time. A full sort would be
	// tidier, but this only runs when the cache is already at its ceiling and
	// the map is being walked anyway.
	for len(m.entries) > limit {
		var oldestKey string
		var oldest time.Time

		for key, e := range m.entries {
			if oldest.IsZero() || e.touched.Before(oldest) {
				oldestKey, oldest = key, e.touched
			}
		}
		if oldestKey == "" {
			return
		}
		delete(m.entries, oldestKey)
	}
}
