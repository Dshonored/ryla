// Package cache stores computed values so they need not be computed again.
//
// Two drivers ship: an in-memory store, which is per-process and disappears on
// restart, and a database store, which is shared between instances and
// survives one. Neither is Redis; when you need Redis you will know, and the
// Store interface is what makes swapping in one a contained change.
//
// A cache is an optimisation, never a source of truth. Every operation here
// treats a failure as a miss rather than an error worth failing a request over:
// a broken cache should make an application slow, not broken.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"golang.org/x/sync/singleflight"
)

// Forever is a TTL meaning "until something evicts it".
const Forever time.Duration = 0

// Store keeps bytes under a key.
type Store interface {
	// Get returns the stored value and whether it was present and unexpired.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores a value. A ttl of Forever means no expiry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes a key. Removing a key that is not there is not an error.
	Delete(ctx context.Context, key string) error
	// Clear empties the store.
	Clear(ctx context.Context) error
}

// ErrNotFound is returned by helpers that need a value to exist.
var ErrNotFound = errors.New("cache: key not found")

// GetJSON reads a value and decodes it into dst.
func GetJSON(ctx context.Context, s Store, key string, dst any) (bool, error) {
	raw, ok, err := s.Get(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		// A value that no longer decodes is almost always a struct that changed
		// shape since it was cached. Dropping it turns a permanent error into
		// one slow request.
		_ = s.Delete(ctx, key)
		return false, nil
	}
	return true, nil
}

// SetJSON encodes a value and stores it.
func SetJSON(ctx context.Context, s Store, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: encode %s: %w", key, err)
	}
	return s.Set(ctx, key, raw, ttl)
}

// group collapses concurrent Remember calls for the same key.
var group singleflight.Group

// Remember returns the cached value for key, computing and storing it on a miss.
//
// Concurrent calls for the same key are collapsed into one computation. Without
// that, a popular key expiring means every in-flight request recomputes it at
// once — a cache stampede, where the cache expiring is what takes the database
// down.
//
// A cache failure is never fatal: if the store cannot be read or written, the
// value is computed and returned anyway.
func Remember[T any](
	ctx context.Context,
	s Store,
	key string,
	ttl time.Duration,
	compute func(context.Context) (T, error),
) (T, error) {
	var zero T

	var cached T
	if ok, _ := GetJSON(ctx, s, key, &cached); ok {
		return cached, nil
	}

	value, err, _ := group.Do(key, func() (any, error) {
		// Check again inside the group: by the time this runs, the call that
		// won the race may already have stored the value.
		var fresh T
		if ok, _ := GetJSON(ctx, s, key, &fresh); ok {
			return fresh, nil
		}

		computed, err := compute(ctx)
		if err != nil {
			return nil, err
		}
		// A failed write is not the caller's problem; they asked for a value,
		// and they have one.
		_ = SetJSON(ctx, s, key, computed, ttl)
		return computed, nil
	})
	if err != nil {
		return zero, err
	}

	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("cache: %s holds %T, not %T", key, value, zero)
	}
	return typed, nil
}

// Jitter spreads a TTL by up to the given fraction.
//
// Caching a batch of keys with an identical TTL means they all expire in the
// same second, and everything that depended on them recomputes together.
// Spreading the expiry avoids that pile-up.
func Jitter(ttl time.Duration, fraction float64) time.Duration {
	if ttl <= 0 || fraction <= 0 {
		return ttl
	}
	spread := float64(ttl) * fraction
	return ttl + time.Duration(rand.Float64()*spread) //nolint:gosec // not security
}
