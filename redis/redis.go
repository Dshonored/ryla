// Package redis opens the shared Redis connection.
//
// Redis is not a primary datastore here. It backs the three things that benefit
// most from being fast and shared between instances — cache, sessions and the
// queue — while the application's own data stays in SQL.
//
// Nothing in the framework imports this package. An application that does not
// use Redis never links the client, which is why the drivers live in
// cache/redis, session/redis and queue/redis rather than alongside their
// interfaces.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the Redis client, aliased so applications need not import the
// driver directly for the common case.
type Client = redis.Client

// Config describes a Redis server.
type Config struct {
	// URL is a redis:// or rediss:// connection string. When set it takes
	// precedence over every other field, because that is the form hosting
	// providers hand out.
	URL string

	Addr     string
	Username string
	Password string
	DB       int

	// PoolSize defaults to ten connections per CPU, which is go-redis's own
	// default and ample for a single application.
	PoolSize int
	// Prefix is prepended to every key. It lets several applications, or
	// several environments, share one Redis without colliding.
	Prefix string

	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Open connects to Redis and verifies the connection.
//
// The ping is deliberate: a misconfigured address would otherwise surface as a
// failure on the first cache read, long after start-up, in whichever request
// happened to be unlucky.
func Open(ctx context.Context, cfg Config) (*redis.Client, error) {
	var opts *redis.Options

	if cfg.URL != "" {
		parsed, err := redis.ParseURL(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("redis: parse URL: %w", err)
		}
		opts = parsed
	} else {
		addr := cfg.Addr
		if addr == "" {
			addr = "localhost:6379"
		}
		opts = &redis.Options{
			Addr:     addr,
			Username: cfg.Username,
			Password: cfg.Password,
			DB:       cfg.DB,
		}
	}

	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}
	if cfg.DialTimeout > 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		opts.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		opts.WriteTimeout = cfg.WriteTimeout
	}

	client := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: connect to %s: %w", opts.Addr, err)
	}
	return client, nil
}

// Key joins a prefix and the parts of a key.
func Key(prefix string, parts ...string) string {
	out := prefix
	for _, p := range parts {
		if out != "" {
			out += ":"
		}
		out += p
	}
	return out
}
