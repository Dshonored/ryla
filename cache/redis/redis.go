// Package redis stores cache entries in Redis.
//
// Unlike the memory store, entries are shared between instances and survive a
// restart; unlike the database store, reads do not touch the primary database.
// This is what most applications want once they run as more than one process.
package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Dshonored/ryla/cache"
)

// Store implements cache.Store on top of Redis.
type Store struct {
	Client *goredis.Client
	// Prefix namespaces every key, so several applications can share one
	// server without colliding.
	Prefix string
}

// New builds a Redis cache store.
func New(client *goredis.Client, prefix string) *Store {
	return &Store{Client: client, Prefix: prefix}
}

func (s *Store) key(k string) string {
	if s.Prefix == "" {
		return k
	}
	return s.Prefix + ":" + k
}

// Get implements cache.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := s.Client.Get(ctx, s.key(key)).Bytes()

	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// Set implements cache.Store.
//
// Redis expires keys itself, so unlike the database store there is nothing to
// prune and no expired row to trip over.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	// A zero expiration means no expiry in go-redis, which is exactly what
	// cache.Forever asks for.
	return s.Client.Set(ctx, s.key(key), value, ttl).Err()
}

// Delete implements cache.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.Client.Del(ctx, s.key(key)).Err()
}

// Clear implements cache.Store.
//
// Only this store's own keys are removed. FLUSHDB would be simpler and would
// also wipe sessions, queued jobs, and anything else sharing the server —
// which is a very bad surprise for a method named Clear.
func (s *Store) Clear(ctx context.Context) error {
	pattern := "*"
	if s.Prefix != "" {
		pattern = s.Prefix + ":*"
	}

	// SCAN rather than KEYS: KEYS blocks the server for the whole sweep, and a
	// cache is exactly where a lot of keys accumulate.
	iter := s.Client.Scan(ctx, 0, pattern, 100).Iterator()

	var batch []string
	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= 100 {
			if err := s.Client.Del(ctx, batch...).Err(); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if len(batch) > 0 {
		return s.Client.Del(ctx, batch...).Err()
	}
	return nil
}

// compile-time check that the store satisfies the interface.
var _ cache.Store = (*Store)(nil)
