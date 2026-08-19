// Package redis stores sessions in Redis.
//
// Like the database store, only the session id lives in the cookie, so a
// session can be revoked by deleting its key — what "log out everywhere" needs.
// Unlike it, reads never touch the primary database, and Redis expires the key
// itself rather than leaving rows to prune.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/session"
)

// Store implements session.Store on top of Redis.
type Store struct {
	Client *goredis.Client
	Jar    *cookie.Jar
	TTL    time.Duration
	Prefix string
}

// New builds a Redis session store.
func New(client *goredis.Client, jar *cookie.Jar, ttl time.Duration, prefix string) *Store {
	if prefix == "" {
		prefix = "session"
	}
	return &Store{Client: client, Jar: jar, TTL: ttl, Prefix: prefix}
}

// Lifetime implements session.Store.
func (s *Store) Lifetime() time.Duration { return s.TTL }

func (s *Store) key(id string) string { return s.Prefix + ":" + id }

// userKey indexes a user's sessions, so every one of them can be ended at once.
func (s *Store) userKey(userID string) string { return s.Prefix + ":user:" + userID }

type payload struct {
	Data      map[string]string `json:"d"`
	ExpiresAt time.Time         `json:"e"`
}

// Load implements session.Store.
func (s *Store) Load(r *http.Request) (*session.Session, error) {
	id, err := s.Jar.Get(r, session.CookieName)
	if err != nil || id == "" {
		return nil, session.ErrNotFound
	}

	raw, err := s.Client.Get(r.Context(), s.key(id)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, session.ErrNotFound
	}
	if p.Data == nil {
		p.Data = map[string]string{}
	}

	sess := &session.Session{ID: id, Data: p.Data, ExpiresAt: p.ExpiresAt}
	if sess.Expired() {
		return nil, session.ErrNotFound
	}
	return sess, nil
}

// Save implements session.Store.
func (s *Store) Save(w http.ResponseWriter, r *http.Request, sess *session.Session) error {
	raw, err := json.Marshal(payload{Data: sess.Data, ExpiresAt: sess.ExpiresAt})
	if err != nil {
		return err
	}

	ttl := time.Until(sess.ExpiresAt)
	if ttl <= 0 {
		ttl = s.TTL
	}

	ctx := r.Context()
	if err := s.Client.Set(ctx, s.key(sess.ID), raw, ttl).Err(); err != nil {
		return err
	}

	// Index by user so DestroyForUser can find them. The index outlives the
	// sessions slightly and is pruned as it is read, since a set member for a
	// key that has expired is harmless but should not accumulate.
	if userID := sess.Get(session.UserIDKey); userID != "" {
		if err := s.Client.SAdd(ctx, s.userKey(userID), sess.ID).Err(); err != nil {
			return err
		}
		if err := s.Client.Expire(ctx, s.userKey(userID), ttl+time.Hour).Err(); err != nil {
			return err
		}
	}

	s.Jar.Set(w, session.CookieName, sess.ID, cookie.Options{MaxAge: int(ttl.Seconds())})
	return nil
}

// Destroy implements session.Store.
func (s *Store) Destroy(w http.ResponseWriter, r *http.Request, sess *session.Session) error {
	s.Jar.Delete(w, session.CookieName)
	if sess == nil || sess.ID == "" {
		return nil
	}

	ctx := r.Context()
	if userID := sess.Get(session.UserIDKey); userID != "" {
		_ = s.Client.SRem(ctx, s.userKey(userID), sess.ID).Err()
	}
	return s.Client.Del(ctx, s.key(sess.ID)).Err()
}

// DestroyForUser ends every session belonging to a user. This is "log out
// everywhere", and what a password change should call.
func (s *Store) DestroyForUser(ctx context.Context, userID string) error {
	ids, err := s.Client.SMembers(ctx, s.userKey(userID)).Result()
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, s.key(id))
	}
	if len(keys) > 0 {
		if err := s.Client.Del(ctx, keys...).Err(); err != nil {
			return err
		}
	}
	return s.Client.Del(ctx, s.userKey(userID)).Err()
}

var _ session.Store = (*Store)(nil)
