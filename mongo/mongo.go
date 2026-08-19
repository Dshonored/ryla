// Package mongo is Ryla's MongoDB layer.
//
// GORM is SQL-only, so a MongoDB application does not use it at all. What
// replaces it is deliberately thinner: a typed Collection over the official
// driver, and an index registry in place of migrations. There is no attempt to
// make documents look like rows — an ORM that pretends a document store is a
// relational one produces the worst of both.
//
// The framework itself never imports this package. An application built on SQL
// never links the driver.
package mongo

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// Aliases so applications need not import the driver for everyday work.
type (
	// Database is a MongoDB database handle.
	Database = mongo.Database
	// Client is a MongoDB client.
	Client = mongo.Client
	// ObjectID is MongoDB's default primary key type.
	ObjectID = bson.ObjectID
)

// Config describes a MongoDB deployment.
type Config struct {
	// URI is a mongodb:// or mongodb+srv:// connection string. This is how
	// hosting providers hand out credentials, so it takes precedence.
	URI string
	// Database is the database to use. When the URI names one, this may be
	// left empty.
	Database string

	// Timeout bounds each operation. The driver's default is no timeout at
	// all, which turns a network partition into a request that never returns.
	Timeout time.Duration
	// MaxPoolSize caps concurrent connections.
	MaxPoolSize uint64
}

// Open connects and verifies the connection.
//
// The ping is deliberate. Without it a bad URI surfaces on the first query,
// inside whichever request happens to be unlucky, rather than at start-up where
// it belongs.
func Open(ctx context.Context, cfg Config) (*mongo.Client, *mongo.Database, error) {
	if cfg.URI == "" {
		return nil, nil, fmt.Errorf("mongo: no connection URI")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	opts := options.Client().
		ApplyURI(cfg.URI).
		SetTimeout(timeout)

	if cfg.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(cfg.MaxPoolSize)
	}

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("mongo: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, nil, fmt.Errorf("mongo: ping: %w", err)
	}

	name := cfg.Database
	if name == "" {
		// A URI of the form mongodb://host/appdb names its database; falling
		// back to it saves configuring the same thing twice.
		name = databaseFromURI(cfg.URI)
	}
	if name == "" {
		return nil, nil, fmt.Errorf("mongo: no database name; set MONGO_DATABASE or include one in the URI")
	}

	return client, client.Database(name), nil
}

// databaseFromURI reads the database out of a connection string's path.
func databaseFromURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}
