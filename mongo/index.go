package mongo

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Index describes an index a collection needs.
//
// This is what replaces migrations. A document store has no schema to migrate:
// adding a field means writing documents that have it, and old documents simply
// do not. Indexes are the part that genuinely has to be declared, created
// before they are relied on, and kept in step across environments.
//
// Applying them is idempotent, so unlike a migration there is no ledger of what
// has run — the declaration is the desired state, and applying it repeatedly is
// a no-op.
type Index struct {
	// Collection the index belongs to.
	Collection string
	// Keys is the index specification, such as
	// bson.D{{Key: "email", Value: 1}}. Order matters for a compound index:
	// MongoDB can use a prefix of the keys and nothing else.
	Keys bson.D
	// Name is optional; MongoDB derives one from the keys when it is empty.
	Name string
	// Unique rejects duplicate values.
	Unique bool
	// Sparse skips documents missing the field. Combined with Unique it allows
	// many documents without the field while still requiring uniqueness among
	// those that have it.
	Sparse bool
	// TTL expires documents this long after the indexed date field. Only valid
	// on a single date field, which is how sessions and cache entries clean up
	// after themselves without a sweeper.
	TTL time.Duration
	// Partial restricts the index to documents matching a filter.
	Partial bson.M
}

var (
	mu      sync.Mutex
	indexes []Index
)

// Register declares an index the application needs.
//
// Called from init in the package defining the model, so importing that package
// is enough for `ry migrate` to know about it.
func Register(idx ...Index) {
	mu.Lock()
	defer mu.Unlock()

	for _, i := range idx {
		if i.Collection == "" {
			panic("mongo: index has no collection")
		}
		if len(i.Keys) == 0 {
			panic(fmt.Sprintf("mongo: index on %s has no keys", i.Collection))
		}
		indexes = append(indexes, i)
	}
}

// Registered returns every declared index, sorted by collection then name.
func Registered() []Index {
	mu.Lock()
	defer mu.Unlock()

	out := make([]Index, len(indexes))
	copy(out, indexes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Collection != out[j].Collection {
			return out[i].Collection < out[j].Collection
		}
		return out[i].describe() < out[j].describe()
	})
	return out
}

// reset clears the registry. Tests only.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	indexes = nil
}

// describe renders an index for logs and listings.
func (i Index) describe() string {
	if i.Name != "" {
		return i.Name
	}

	out := ""
	for _, k := range i.Keys {
		if out != "" {
			out += ","
		}
		out += fmt.Sprintf("%s:%v", k.Key, k.Value)
	}
	return out
}

// model builds the driver's index specification.
func (i Index) model() mongo.IndexModel {
	opts := options.Index()

	if i.Name != "" {
		opts.SetName(i.Name)
	}
	if i.Unique {
		opts.SetUnique(true)
	}
	if i.Sparse {
		opts.SetSparse(true)
	}
	if i.TTL > 0 {
		opts.SetExpireAfterSeconds(int32(i.TTL.Seconds()))
	}
	if i.Partial != nil {
		opts.SetPartialFilterExpression(i.Partial)
	}

	return mongo.IndexModel{Keys: i.Keys, Options: opts}
}

// Syncer applies declared indexes to a database.
type Syncer struct {
	DB  *mongo.Database
	Log *slog.Logger
}

// NewSyncer builds a syncer.
func NewSyncer(db *mongo.Database, log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	return &Syncer{DB: db, Log: log}
}

// Sync creates every declared index.
//
// Creating an index that already exists is a no-op, so this is safe to run on
// every deploy — which is the point. An index whose options changed is a
// different matter: MongoDB rejects that rather than silently rebuilding, and
// the error says so, because quietly dropping and recreating an index on a
// large collection is not something a deploy should do without being asked.
func (s *Syncer) Sync(ctx context.Context) error {
	all := Registered()
	if len(all) == 0 {
		s.Log.Info("indexes: nothing declared")
		return nil
	}

	byCollection := map[string][]mongo.IndexModel{}
	for _, idx := range all {
		byCollection[idx.Collection] = append(byCollection[idx.Collection], idx.model())
	}

	names := make([]string, 0, len(byCollection))
	for name := range byCollection {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		start := time.Now()

		created, err := s.DB.Collection(name).Indexes().CreateMany(ctx, byCollection[name])
		if err != nil {
			return fmt.Errorf("mongo: create indexes on %s: %w", name, err)
		}

		s.Log.Info("indexes ensured",
			"collection", name,
			"count", len(created),
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	}
	return nil
}

// Status describes an index that exists on the server.
type Status struct {
	Collection string
	Name       string
	Keys       string
	// Declared is false for an index that exists but is not in the code —
	// usually one created by hand, or left behind by an older deploy.
	Declared bool
}

// Status lists the indexes present on the server, marking which are declared.
func (s *Syncer) Status(ctx context.Context) ([]Status, error) {
	declared := map[string]bool{}
	for _, idx := range Registered() {
		declared[idx.Collection+"/"+idx.describe()] = true
	}

	collections, err := s.DB.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	sort.Strings(collections)

	var out []Status
	for _, name := range collections {
		cursor, err := s.DB.Collection(name).Indexes().List(ctx)
		if err != nil {
			return nil, err
		}

		var specs []bson.M
		if err := cursor.All(ctx, &specs); err != nil {
			return nil, err
		}

		for _, spec := range specs {
			indexName, _ := spec["name"].(string)
			keys := fmt.Sprint(spec["key"])

			out = append(out, Status{
				Collection: name,
				Name:       indexName,
				Keys:       keys,
				// _id is always present and never declared; marking it as
				// undeclared would put a permanent false positive in the list.
				Declared: indexName == "_id_" || declared[name+"/"+indexName],
			})
		}
	}
	return out, nil
}
