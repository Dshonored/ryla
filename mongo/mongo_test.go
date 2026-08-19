package mongo

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// --- unit tests, no server needed -----------------------------------------

func TestDatabaseFromURI(t *testing.T) {
	tests := map[string]string{
		"mongodb://localhost:27017/shop":                 "shop",
		"mongodb://user:pass@localhost:27017/shop?tls=1": "shop",
		"mongodb+srv://cluster.example.net/shop":         "shop",
		"mongodb://localhost:27017":                      "",
		"mongodb://localhost:27017/":                     "",
		"":                                               "",
	}

	for uri, want := range tests {
		if got := databaseFromURI(uri); got != want {
			t.Errorf("databaseFromURI(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestOpenRequiresAURI(t *testing.T) {
	if _, _, err := Open(context.Background(), Config{}); err == nil {
		t.Error("Open with no URI was accepted")
	}
}

func TestIndexDescribe(t *testing.T) {
	tests := []struct {
		idx  Index
		want string
	}{
		{Index{Keys: bson.D{{Key: "email", Value: 1}}}, "email:1"},
		{Index{Keys: bson.D{{Key: "a", Value: 1}, {Key: "b", Value: -1}}}, "a:1,b:-1"},
		{Index{Name: "custom", Keys: bson.D{{Key: "x", Value: 1}}}, "custom"},
	}

	for _, tc := range tests {
		if got := tc.idx.describe(); got != tc.want {
			t.Errorf("describe() = %q, want %q", got, tc.want)
		}
	}
}

func TestRegisterRejectsIncompleteIndexes(t *testing.T) {
	cases := map[string]Index{
		"no collection": {Keys: bson.D{{Key: "a", Value: 1}}},
		"no keys":       {Collection: "users"},
	}

	for name, idx := range cases {
		t.Run(name, func(t *testing.T) {
			reset()
			t.Cleanup(reset)

			defer func() {
				if recover() == nil {
					t.Error("an incomplete index was accepted")
				}
			}()
			Register(idx)
		})
	}
}

func TestRegisteredIsSorted(t *testing.T) {
	reset()
	t.Cleanup(reset)

	Register(
		Index{Collection: "users", Keys: bson.D{{Key: "email", Value: 1}}},
		Index{Collection: "posts", Keys: bson.D{{Key: "slug", Value: 1}}},
		Index{Collection: "users", Keys: bson.D{{Key: "created_at", Value: -1}}},
	)

	got := Registered()
	if len(got) != 3 {
		t.Fatalf("got %d indexes, want 3", len(got))
	}
	// Stable ordering, so `ry migrate` output and index creation do not shuffle
	// between runs.
	if got[0].Collection != "posts" || got[1].Collection != "users" {
		t.Errorf("order = %s, %s, %s", got[0].Collection, got[1].Collection, got[2].Collection)
	}
	if got[1].describe() != "created_at:-1" {
		t.Errorf("within a collection, order = %q", got[1].describe())
	}
}

func TestIndexModelCarriesItsOptions(t *testing.T) {
	idx := Index{
		Collection: "sessions",
		Keys:       bson.D{{Key: "expires_at", Value: 1}},
		TTL:        time.Hour,
		Unique:     true,
		Sparse:     true,
		Name:       "session_ttl",
	}

	model := idx.model()
	if model.Options == nil {
		t.Fatal("no options were built")
	}

	// Driver v2 hands back an opaque builder, so the setters are applied to get
	// at what they would actually send.
	args := &options.IndexOptions{}
	for _, set := range model.Options.List() {
		if err := set(args); err != nil {
			t.Fatalf("apply option: %v", err)
		}
	}

	if args.ExpireAfterSeconds == nil || *args.ExpireAfterSeconds != 3600 {
		t.Errorf("TTL did not become ExpireAfterSeconds: %v", args.ExpireAfterSeconds)
	}
	if args.Unique == nil || !*args.Unique {
		t.Error("Unique was not set")
	}
	if args.Sparse == nil || !*args.Sparse {
		t.Error("Sparse was not set")
	}
	if args.Name == nil || *args.Name != "session_ttl" {
		t.Error("Name was not set")
	}
}

func TestIsDuplicate(t *testing.T) {
	dup := driver.WriteException{
		WriteErrors: []driver.WriteError{{Code: 11000, Message: "duplicate key"}},
	}
	other := driver.WriteException{
		WriteErrors: []driver.WriteError{{Code: 121, Message: "validation failed"}},
	}

	if !IsDuplicate(dup) {
		t.Error("a duplicate-key error was not recognised")
	}
	if IsDuplicate(other) {
		t.Error("an unrelated write error was reported as a duplicate")
	}
	if IsDuplicate(errors.New("network down")) {
		t.Error("a plain error was reported as a duplicate")
	}
	if IsDuplicate(nil) {
		t.Error("nil was reported as a duplicate")
	}
}

func TestTimestamps(t *testing.T) {
	var ts Timestamps
	ts.Touch()

	if ts.CreatedAt.IsZero() || ts.UpdatedAt.IsZero() {
		t.Fatal("Touch left a timestamp unset")
	}

	created := ts.CreatedAt
	time.Sleep(2 * time.Millisecond)
	ts.Touch()

	// Created is set once and never moves; updated moves every time.
	if !ts.CreatedAt.Equal(created) {
		t.Error("CreatedAt changed on a second Touch")
	}
	if !ts.UpdatedAt.After(created) {
		t.Error("UpdatedAt did not move")
	}
}

func TestNowUpdate(t *testing.T) {
	got := NowUpdate(bson.M{"name": "Alice"})

	set, ok := got["$set"].(bson.M)
	if !ok {
		t.Fatalf("NowUpdate = %v, want a $set document", got)
	}
	if set["name"] != "Alice" {
		t.Errorf("the caller's fields were lost: %v", set)
	}
	if _, ok := set["updated_at"]; !ok {
		t.Error("updated_at was not added")
	}

	// A nil map must not panic.
	if _, ok := NowUpdate(nil)["$set"]; !ok {
		t.Error("NowUpdate(nil) did not produce a $set")
	}
}

// --- integration tests ------------------------------------------------------

type person struct {
	ID         bson.ObjectID `bson:"_id,omitempty"`
	Name       string        `bson:"name"`
	Email      string        `bson:"email"`
	Age        int           `bson:"age"`
	Timestamps `bson:",inline"`
}

// live connects to a real MongoDB, or skips.
//
// There is no in-process MongoDB the way miniredis stands in for Redis, so
// these run where a server exists — CI provides one — and are skipped
// elsewhere rather than silently not testing anything.
func live(t *testing.T) *driver.Database {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("skipping: set MONGO_TEST_URI to run the MongoDB integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, db, err := Open(ctx, Config{URI: uri, Database: "ryla_test"})
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	// A fresh database per test, so one leaving documents behind cannot change
	// what the next one sees.
	if err := db.Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	return db
}

func TestLiveCRUD(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	people := For[person](db, "people")

	alice := person{Name: "Alice", Email: "alice@example.com", Age: 30}
	alice.Touch()

	id, err := people.Insert(ctx, &alice)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := people.FindByID(ctx, id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "Alice" || got.Age != 30 {
		t.Errorf("read back %+v", got)
	}

	changed, err := people.Set(ctx, Filter{"_id": id}, bson.M{"age": 31})
	if err != nil || !changed {
		t.Fatalf("Set = %v, %v", changed, err)
	}

	got, _ = people.FindByID(ctx, id)
	if got.Age != 31 {
		t.Errorf("age = %d, want 31", got.Age)
	}

	deleted, err := people.DeleteByID(ctx, id)
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}

	if _, err := people.FindByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID after delete = %v, want ErrNotFound", err)
	}
}

func TestLiveFindWithQuery(t *testing.T) {
	db := live(t)
	ctx := context.Background()
	people := For[person](db, "people")

	for i, name := range []string{"Alice", "Bob", "Carol"} {
		p := person{Name: name, Email: strings.ToLower(name) + "@example.com", Age: 20 + i*10}
		p.Touch()
		if _, err := people.Insert(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}

	got, err := people.Find(ctx, Filter{"age": bson.M{"$gte": 30}}, Query{
		Sort:  bson.D{{Key: "age", Value: -1}},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 || got[0].Name != "Carol" {
		t.Errorf("got %+v", got)
	}

	n, err := people.Count(ctx, Filter{})
	if err != nil || n != 3 {
		t.Errorf("Count = %d, %v", n, err)
	}

	exists, err := people.Exists(ctx, Filter{"name": "Bob"})
	if err != nil || !exists {
		t.Errorf("Exists = %v, %v", exists, err)
	}
	if exists, _ := people.Exists(ctx, Filter{"name": "Nobody"}); exists {
		t.Error("Exists reported a match for a missing document")
	}
}

// TestLiveUniqueIndexRejectsDuplicates is what replaces a unique constraint:
// the index is what actually guarantees it, since two concurrent writers can
// both pass an application-level check.
func TestLiveUniqueIndexRejectsDuplicates(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	reset()
	t.Cleanup(reset)
	Register(Index{
		Collection: "people",
		Keys:       bson.D{{Key: "email", Value: 1}},
		Unique:     true,
	})

	if err := NewSyncer(db, quietLogger()).Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	people := For[person](db, "people")
	first := person{Name: "Alice", Email: "same@example.com"}
	if _, err := people.Insert(ctx, &first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	second := person{Name: "Mallory", Email: "same@example.com"}
	_, err := people.Insert(ctx, &second)
	if err == nil {
		t.Fatal("a duplicate email was accepted")
	}
	if !IsDuplicate(err) {
		t.Errorf("IsDuplicate did not recognise %v", err)
	}
}

func TestLiveSyncIsIdempotent(t *testing.T) {
	db := live(t)
	ctx := context.Background()

	reset()
	t.Cleanup(reset)
	Register(Index{Collection: "people", Keys: bson.D{{Key: "email", Value: 1}}, Unique: true})

	s := NewSyncer(db, quietLogger())

	// Applying the declared state repeatedly has to be a no-op, because it runs
	// on every deploy.
	for i := 0; i < 3; i++ {
		if err := s.Sync(ctx); err != nil {
			t.Fatalf("Sync %d: %v", i+1, err)
		}
	}

	status, err := s.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	declared := 0
	for _, st := range status {
		if st.Collection == "people" && st.Name != "_id_" {
			declared++
		}
	}
	if declared != 1 {
		t.Errorf("%d indexes on people after three syncs, want 1", declared)
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
