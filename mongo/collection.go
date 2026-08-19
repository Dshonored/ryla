package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrNotFound is returned when a query matched no document.
//
// It wraps the driver's own sentinel, so code that already checks for
// mongo.ErrNoDocuments keeps working, while application code can use one error
// that does not leak the driver.
var ErrNotFound = errors.New("mongo: no document found")

// Filter is a query. It is bson.M — a plain map — rather than a builder API on
// purpose: MongoDB queries are documents, every example in the manual is a
// document, and a fluent wrapper only adds a dialect to translate from.
type Filter = bson.M

// Update is a modification document, such as bson.M{"$set": ...}.
type Update = bson.M

// Collection is a typed view over one MongoDB collection.
//
// The generic parameter is the document type. It buys the thing an untyped
// driver cannot: the compiler checking that what is written and what is read
// are the same shape.
type Collection[T any] struct {
	C *mongo.Collection
}

// For returns a typed collection by name.
func For[T any](db *mongo.Database, name string) *Collection[T] {
	return &Collection[T]{C: db.Collection(name)}
}

// Insert stores one document and returns its id.
func (c *Collection[T]) Insert(ctx context.Context, doc *T) (bson.ObjectID, error) {
	res, err := c.C.InsertOne(ctx, doc)
	if err != nil {
		return bson.NilObjectID, err
	}

	id, ok := res.InsertedID.(bson.ObjectID)
	if !ok {
		// The document carried its own _id of another type, which is allowed.
		return bson.NilObjectID, nil
	}
	return id, nil
}

// InsertMany stores several documents.
func (c *Collection[T]) InsertMany(ctx context.Context, docs []T) error {
	if len(docs) == 0 {
		return nil
	}

	any := make([]any, len(docs))
	for i := range docs {
		any[i] = docs[i]
	}
	_, err := c.C.InsertMany(ctx, any)
	return err
}

// FindOne returns the first document matching filter, or ErrNotFound.
func (c *Collection[T]) FindOne(ctx context.Context, filter Filter) (*T, error) {
	var out T

	err := c.C.FindOne(ctx, filter).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// FindByID returns the document with the given id.
func (c *Collection[T]) FindByID(ctx context.Context, id bson.ObjectID) (*T, error) {
	return c.FindOne(ctx, Filter{"_id": id})
}

// Query describes a multi-document read.
type Query struct {
	// Sort is a document such as bson.M{"created_at": -1}.
	Sort bson.D
	// Limit caps the results. Zero means no limit — which is almost never what
	// a request handler wants, so set it.
	Limit int64
	// Skip is an offset. Beware of it on large collections: MongoDB walks every
	// skipped document, so a deep page is slow. Prefer a range on an indexed
	// field.
	Skip int64
	// Projection limits the fields returned.
	Projection bson.M
}

// Find returns every document matching filter.
func (c *Collection[T]) Find(ctx context.Context, filter Filter, q ...Query) ([]T, error) {
	opts := options.Find()

	if len(q) > 0 {
		if len(q[0].Sort) > 0 {
			opts.SetSort(q[0].Sort)
		}
		if q[0].Limit > 0 {
			opts.SetLimit(q[0].Limit)
		}
		if q[0].Skip > 0 {
			opts.SetSkip(q[0].Skip)
		}
		if q[0].Projection != nil {
			opts.SetProjection(q[0].Projection)
		}
	}

	cursor, err := c.C.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var out []T
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Count returns how many documents match.
func (c *Collection[T]) Count(ctx context.Context, filter Filter) (int64, error) {
	return c.C.CountDocuments(ctx, filter)
}

// Exists reports whether any document matches.
//
// Cheaper than Count on a large match: it stops at the first hit rather than
// walking every one.
func (c *Collection[T]) Exists(ctx context.Context, filter Filter) (bool, error) {
	err := c.C.FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()

	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Update applies a modification to the first matching document and reports
// whether anything changed.
func (c *Collection[T]) Update(ctx context.Context, filter Filter, update Update) (bool, error) {
	res, err := c.C.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// UpdateByID applies a modification to one document by id.
func (c *Collection[T]) UpdateByID(ctx context.Context, id bson.ObjectID, update Update) (bool, error) {
	return c.Update(ctx, Filter{"_id": id}, update)
}

// Set is shorthand for an update that assigns fields.
func (c *Collection[T]) Set(ctx context.Context, filter Filter, fields bson.M) (bool, error) {
	return c.Update(ctx, filter, Update{"$set": fields})
}

// UpdateMany applies a modification to every matching document and returns how
// many were changed.
func (c *Collection[T]) UpdateMany(ctx context.Context, filter Filter, update Update) (int64, error) {
	res, err := c.C.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

// Upsert inserts the document or updates it if the filter matches.
func (c *Collection[T]) Upsert(ctx context.Context, filter Filter, update Update) error {
	_, err := c.C.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

// Delete removes the first matching document and reports whether one was
// removed.
func (c *Collection[T]) Delete(ctx context.Context, filter Filter) (bool, error) {
	res, err := c.C.DeleteOne(ctx, filter)
	if err != nil {
		return false, err
	}
	return res.DeletedCount > 0, nil
}

// DeleteByID removes one document by id.
func (c *Collection[T]) DeleteByID(ctx context.Context, id bson.ObjectID) (bool, error) {
	return c.Delete(ctx, Filter{"_id": id})
}

// DeleteMany removes every matching document and returns how many went.
func (c *Collection[T]) DeleteMany(ctx context.Context, filter Filter) (int64, error) {
	res, err := c.C.DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}

// IsDuplicate reports whether err is a unique index rejecting a duplicate.
//
// This is the document-store equivalent of a unique constraint violation, and
// the same rule applies: check first for a clear message, but let the index be
// what actually guarantees it, since two concurrent writers can both pass the
// check.
func IsDuplicate(err error) bool {
	var writeErr mongo.WriteException
	if errors.As(err, &writeErr) {
		for _, e := range writeErr.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}

	var bulkErr mongo.BulkWriteException
	if errors.As(err, &bulkErr) {
		for _, e := range bulkErr.WriteErrors {
			if e.Code == 11000 {
				return true
			}
		}
	}
	return false
}

// Timestamps is embedded in documents that want created and updated times.
type Timestamps struct {
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// Touch sets both timestamps for a new document.
func (t *Timestamps) Touch() {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
}

// NowUpdate returns a $set document that refreshes updated_at alongside fields.
func NowUpdate(fields bson.M) Update {
	if fields == nil {
		fields = bson.M{}
	}
	fields["updated_at"] = time.Now().UTC()
	return Update{"$set": fields}
}

// Ping reports whether the database is reachable, for a health endpoint.
func Ping(ctx context.Context, db *mongo.Database) error {
	if db == nil {
		return fmt.Errorf("mongo: no database")
	}
	return db.Client().Ping(ctx, nil)
}
