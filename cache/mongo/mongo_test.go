package mongo

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Dshonored/ryla/cache"
	rymongo "github.com/Dshonored/ryla/mongo"
)

// --- unit tests, no server needed -----------------------------------------

// TestKeysAreNamespaced protects the property that two applications sharing one
// database do not read each other's entries.
func TestKeysAreNamespaced(t *testing.T) {
	prefixed := &Store{Prefix: "shop"}
	if got := prefixed.key("user:1"); got != "shop:user:1" {
		t.Errorf("key = %q, want %q", got, "shop:user:1")
	}

	// An empty prefix has to leave the key alone rather than produce a leading
	// separator, or the same key means two different things depending on how the
	// store was built.
	bare := &Store{}
	if got := bare.key("user:1"); got != "user:1" {
		t.Errorf("key = %q, want %q", got, "user:1")
	}
}

// TestTTLIndexIsDeclared protects the thing that makes expiry the server's job:
// if the declaration is lost, entries accumulate forever and nothing fails
// loudly enough to notice.
func TestTTLIndexIsDeclared(t *testing.T) {
	var ttl, unique bool

	for _, idx := range rymongo.Registered() {
		if idx.Collection != CollectionName {
			continue
		}
		switch idx.Keys[0].Key {
		case "expires_at":
			// Non-zero, because the registry reads a zero TTL as "no expiry" and
			// would leave expireAfterSeconds off the index entirely.
			if idx.TTL <= 0 {
				t.Errorf("the expires_at index has TTL %v, so it would not expire anything", idx.TTL)
			}
			ttl = true
		case "key":
			if !idx.Unique {
				t.Error("the key index is not unique, so concurrent upserts can duplicate an entry")
			}
			unique = true
		}
	}

	if !ttl {
		t.Error("no TTL index on expires_at was registered")
	}
	if !unique {
		t.Error("no unique index on key was registered")
	}
}

// --- integration tests ------------------------------------------------------

// live connects to a real MongoDB, or skips.
//
// There is no in-process MongoDB the way miniredis stands in for Redis, so these
// run where a server exists — CI provides one — and are skipped elsewhere rather
// than silently not testing anything.
func live(t *testing.T) *rymongo.Database {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("skipping: set MONGO_TEST_URI to run the MongoDB integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, db, err := rymongo.Open(ctx, rymongo.Config{URI: uri, Database: "ryla_cache_test"})
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	// A fresh database per test, so one leaving entries behind cannot change what
	// the next one sees.
	if err := db.Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	return db
}

func TestLiveGetSetDelete(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	var store cache.Store = New(db, "test")

	if _, ok, err := store.Get(ctx, "missing"); ok || err != nil {
		t.Fatalf("Get on an empty store = %v, %v, want a clean miss", ok, err)
	}

	if err := store.Set(ctx, "greeting", []byte("hello"), cache.Forever); err != nil {
		t.Fatalf("Set: %v", err)
	}

	value, ok, err := store.Get(ctx, "greeting")
	if err != nil || !ok {
		t.Fatalf("Get = %v, %v", ok, err)
	}
	if string(value) != "hello" {
		t.Errorf("value = %q, want %q", value, "hello")
	}

	if err := store.Delete(ctx, "greeting"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := store.Get(ctx, "greeting"); ok {
		t.Error("a deleted key was still served")
	}

	// Deleting a key that is not there is not an error, because callers
	// invalidate keys they cannot know the state of.
	if err := store.Delete(ctx, "never-existed"); err != nil {
		t.Errorf("Delete of a missing key = %v", err)
	}
}

// TestLiveSetOverwrites protects the upsert: re-caching a key is the normal case
// for a cache, and an insert that conflicted instead would fail every refresh
// after the first.
func TestLiveSetOverwrites(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	store := New(db, "test")

	for _, want := range []string{"first", "second", "third"} {
		if err := store.Set(ctx, "k", []byte(want), time.Minute); err != nil {
			t.Fatalf("Set %s: %v", want, err)
		}

		got, ok, err := store.Get(ctx, "k")
		if err != nil || !ok {
			t.Fatalf("Get after setting %s = %v, %v", want, ok, err)
		}
		if string(got) != want {
			t.Errorf("value = %q, want %q", got, want)
		}
	}

	// One key, one document — a second document would mean Get returns whichever
	// the server reached first.
	n, err := store.docs.Count(ctx, rymongo.Filter{"key": "test:k"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d documents for one key, want 1", n)
	}
}

// TestLiveExpiredEntryIsNotServed is the important one. MongoDB's TTL reaper
// runs about once a minute, so for most of that minute an expired entry is still
// sitting in the collection; Get has to check the deadline itself rather than
// trust that the server has already removed it.
func TestLiveExpiredEntryIsNotServed(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	store := New(db, "test")

	if err := store.Set(ctx, "brief", []byte("value"), 40*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, ok, _ := store.Get(ctx, "brief"); !ok {
		t.Fatal("the entry was not readable before it expired")
	}

	time.Sleep(80 * time.Millisecond)

	// Far sooner than the reaper could have run, so this only passes if Get
	// enforced the deadline.
	if _, ok, err := store.Get(ctx, "brief"); ok || err != nil {
		t.Errorf("Get after expiry = %v, %v, want a clean miss", ok, err)
	}

	// And it is gone, not merely hidden, so keys nobody asks for again do not
	// wait on the reaper either.
	n, err := store.docs.Count(ctx, rymongo.Filter{"key": "test:brief"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d expired documents left behind, want 0", n)
	}
}

// TestLiveForeverEntryHasNoDeadline protects entries stored with cache.Forever
// from the TTL index: they must carry a null expires_at, which the reaper skips,
// rather than a date it would eventually collect.
func TestLiveForeverEntryHasNoDeadline(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	store := New(db, "test")

	if err := store.Set(ctx, "permanent", []byte("value"), cache.Forever); err != nil {
		t.Fatalf("Set: %v", err)
	}

	doc, err := store.docs.FindOne(ctx, rymongo.Filter{"key": "test:permanent"})
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if doc.ExpiresAt != nil {
		t.Errorf("a Forever entry was given a deadline of %v", *doc.ExpiresAt)
	}

	// Re-caching a key as Forever has to clear the old deadline, or the entry
	// still expires on the previous write's terms.
	if err := store.Set(ctx, "temporary", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set with a TTL: %v", err)
	}
	if err := store.Set(ctx, "temporary", []byte("value"), cache.Forever); err != nil {
		t.Fatalf("Set as Forever: %v", err)
	}

	doc, err = store.docs.FindOne(ctx, rymongo.Filter{"key": "test:temporary"})
	if err != nil {
		t.Fatalf("FindOne: %v", err)
	}
	if doc.ExpiresAt != nil {
		t.Errorf("the previous deadline of %v survived a Forever write", *doc.ExpiresAt)
	}
}

// TestLiveEmptyValueIsAHit separates "cached as empty" from "not cached".
// Caching the absence of something is a real use, and a store that reported it
// as a miss would recompute it on every request.
func TestLiveEmptyValueIsAHit(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	store := New(db, "test")

	if err := store.Set(ctx, "empty", []byte{}, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	value, ok, err := store.Get(ctx, "empty")
	if err != nil || !ok {
		t.Fatalf("Get = %v, %v, want a hit", ok, err)
	}
	if len(value) != 0 {
		t.Errorf("value = %q, want empty", value)
	}
}

// TestLiveClearLeavesEverythingElseAlone protects against the obvious shortcut.
// Dropping the collection or the database would be simpler and would also take
// another application's cache, and every other collection, with it.
func TestLiveClearLeavesEverythingElseAlone(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	shop := New(db, "shop")
	blog := New(db, "blog")

	if err := shop.Set(ctx, "k", []byte("shop"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := blog.Set(ctx, "k", []byte("blog"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// An unrelated collection in the same database, standing in for sessions,
	// queued jobs, and application data.
	if _, err := db.Collection("people").InsertOne(ctx, bson.M{"name": "Alice"}); err != nil {
		t.Fatalf("insert unrelated document: %v", err)
	}

	if err := shop.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if _, ok, _ := shop.Get(ctx, "k"); ok {
		t.Error("Clear left this store's own entry behind")
	}

	value, ok, err := blog.Get(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("the other store's entry = %v, %v, want a hit", ok, err)
	}
	if string(value) != "blog" {
		t.Errorf("the other store's value = %q", value)
	}

	n, err := db.Collection("people").CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count unrelated documents: %v", err)
	}
	if n != 1 {
		t.Errorf("%d unrelated documents survived Clear, want 1", n)
	}
}

// TestLiveTTLIndexIsCreated checks the declaration survives contact with a real
// server: an index MongoDB rejects is one the application only finds out about
// when the collection has grown without bound.
func TestLiveTTLIndexIsCreated(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	if err := rymongo.NewSyncer(db, quietLogger()).Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	cursor, err := db.Collection(CollectionName).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var specs []bson.M
	if err := cursor.All(ctx, &specs); err != nil {
		t.Fatalf("read indexes: %v", err)
	}

	var found bool
	for _, spec := range specs {
		if spec["name"] != "cache_ttl" {
			continue
		}
		found = true
		if _, ok := spec["expireAfterSeconds"]; !ok {
			t.Errorf("cache_ttl exists but is not a TTL index: %v", spec)
		}
	}
	if !found {
		t.Errorf("no cache_ttl index on the server; got %v", specs)
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
