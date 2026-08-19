// Package mongo stores cache entries in MongoDB.
//
// The database store speaks SQL through GORM, which a MongoDB application never
// links at all, so this is its counterpart: entries are shared between
// instances, survive a restart, and reads never touch a relational database
// that is not there.
//
// Unlike the SQL store there is nothing to prune on a schedule. A TTL index has
// the server remove expired entries on its own, and Get drops anything that
// outlived its deadline as it finds it.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Dshonored/ryla/cache"
	rymongo "github.com/Dshonored/ryla/mongo"
)

// CollectionName is the collection cache entries live in.
//
// Fixed rather than configurable because the TTL index is declared at init
// against this name; a store pointed at some other collection would quietly run
// without the index that makes expiry work.
const CollectionName = "cache"

// document is one cache entry.
type document struct {
	Key   string `bson:"key"`
	Value []byte `bson:"value"`
	// ExpiresAt is nil for cache.Forever. MongoDB's TTL reaper skips documents
	// whose indexed field is not a date, so a null here means "never expires"
	// without needing a second index or a partial filter.
	ExpiresAt *time.Time `bson:"expires_at"`
}

func init() {
	rymongo.Register(
		// The unique index is what actually stops a duplicate: two instances can
		// upsert the same key at the same moment, and without it both inserts
		// succeed and Get starts returning whichever document it reaches first.
		rymongo.Index{
			Collection: CollectionName,
			Keys:       bson.D{{Key: "key", Value: 1}},
			Name:       "cache_key",
			Unique:     true,
		},
		// A TTL index expires each entry at its own expires_at, so no sweeper
		// has to run. The reaper only wakes about once a minute, though, which
		// is why Get checks the deadline itself rather than trusting it —
		// otherwise an entry that expired thirty seconds ago is still served.
		//
		// One second, not zero: the registry reads a zero TTL as "no expiry at
		// all" and leaves expireAfterSeconds off, and a second of slack past a
		// deadline Get already enforces costs nothing.
		rymongo.Index{
			Collection: CollectionName,
			Keys:       bson.D{{Key: "expires_at", Value: 1}},
			Name:       "cache_ttl",
			TTL:        time.Second,
		},
	)
}

// Store implements cache.Store on top of MongoDB.
type Store struct {
	// Prefix namespaces every key, so several caches can share one collection
	// without colliding.
	Prefix string

	docs *rymongo.Collection[document]
}

// New builds a MongoDB cache store.
func New(db *rymongo.Database, prefix string) *Store {
	return &Store{Prefix: prefix, docs: rymongo.For[document](db, CollectionName)}
}

func (s *Store) key(k string) string {
	if s.Prefix == "" {
		return k
	}
	return s.Prefix + ":" + k
}

// Get implements cache.Store.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	doc, err := s.docs.FindOne(ctx, rymongo.Filter{"key": s.key(key)})

	if errors.Is(err, rymongo.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cache/mongo: get %s: %w", key, err)
	}

	if doc.ExpiresAt != nil && time.Now().After(*doc.ExpiresAt) {
		// The TTL reaper has not come round yet. Cleaned up as it is found, the
		// way the SQL store does, so a key nobody reads again still goes.
		_ = s.Delete(ctx, key)
		return nil, false, nil
	}
	return doc.Value, true, nil
}

// Set implements cache.Store.
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	fields := bson.M{
		"key":   s.key(key),
		"value": value,
		// Overwritten below when there is a TTL. Written unconditionally so that
		// re-caching a key as Forever clears an old deadline instead of leaving
		// the entry to expire on the previous write's terms.
		"expires_at": nil,
	}
	if ttl > 0 {
		fields["expires_at"] = time.Now().UTC().Add(ttl)
	}

	// Upsert, because writing a key that already exists is the normal case for a
	// cache, not a conflict worth reporting.
	if err := s.docs.Upsert(ctx, rymongo.Filter{"key": s.key(key)}, rymongo.Update{"$set": fields}); err != nil {
		return fmt.Errorf("cache/mongo: set %s: %w", key, err)
	}
	return nil
}

// Delete implements cache.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.docs.Delete(ctx, rymongo.Filter{"key": s.key(key)}); err != nil {
		return fmt.Errorf("cache/mongo: delete %s: %w", key, err)
	}
	return nil
}

// Clear implements cache.Store.
//
// Only this store's own documents are removed. Dropping the collection, let
// alone the database, would also take sessions, queued jobs, and application
// data with it — a very bad surprise for a method named Clear.
func (s *Store) Clear(ctx context.Context) error {
	filter := rymongo.Filter{}
	if s.Prefix != "" {
		// Anchored, so MongoDB can walk the key index for the prefix range
		// rather than reading every document. The prefix is escaped because a
		// regex metacharacter in it would otherwise widen the match into keys
		// belonging to another store.
		filter["key"] = bson.M{"$regex": "^" + regexp.QuoteMeta(s.Prefix+":")}
	}

	if _, err := s.docs.DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("cache/mongo: clear: %w", err)
	}
	return nil
}

// compile-time check that the store satisfies the interface.
var _ cache.Store = (*Store)(nil)
