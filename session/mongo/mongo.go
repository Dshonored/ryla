// Package mongo stores sessions in MongoDB.
//
// Like the database store, only the session id lives in the cookie and the data
// stays on the server, which is what makes a session revocable: deleting the
// document ends it immediately, however many browsers hold the cookie. Unlike
// it, nothing here touches a SQL database, and expired sessions are collected by
// a TTL index rather than a pruning job.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Dshonored/ryla/cookie"
	rylamongo "github.com/Dshonored/ryla/mongo"
	"github.com/Dshonored/ryla/session"
)

// CollectionName is the collection sessions are stored in.
const CollectionName = "sessions"

func init() {
	rylamongo.Register(
		// MongoDB removes a document TTL seconds after the indexed date, so the
		// smallest positive value makes expires_at an exact deadline rather than
		// an offset. Zero would mean exactly that, but the registry reads zero as
		// "no TTL", so a second is as close as it can be asked for. It is not
		// precise anyway: the reaper runs roughly once a minute, which is why
		// Load checks the expiry itself.
		rylamongo.Index{
			Collection: CollectionName,
			Keys:       bson.D{{Key: "expires_at", Value: 1}},
			Name:       "sessions_expires_at_ttl",
			TTL:        time.Second,
		},
		// Sparse, because an anonymous session carries no user_id at all and
		// there is no point indexing every crawler that ever visited. This index
		// is what makes DestroyForUser — "log out everywhere" — affordable.
		rylamongo.Index{
			Collection: CollectionName,
			Keys:       bson.D{{Key: "user_id", Value: 1}},
			Name:       "sessions_user_id",
			Sparse:     true,
		},
	)
}

// Document is one session as it is stored.
//
// The session id is the document's _id: it is already unique and already random,
// so a separate key would only add an index to keep in step. Data is a
// subdocument rather than an encoded blob, so a support question about what a
// session holds can be answered from the shell.
type Document struct {
	ID        string            `bson:"_id"`
	Data      map[string]string `bson:"data"`
	UserID    string            `bson:"user_id,omitempty"`
	ExpiresAt time.Time         `bson:"expires_at"`
	UpdatedAt time.Time         `bson:"updated_at"`
}

// Store implements session.Store on top of MongoDB.
type Store struct {
	Sessions *rylamongo.Collection[Document]
	Jar      *cookie.Jar
	TTL      time.Duration
}

// New builds a MongoDB session store.
func New(db *rylamongo.Database, jar *cookie.Jar, ttl time.Duration) *Store {
	return &Store{
		Sessions: rylamongo.For[Document](db, CollectionName),
		Jar:      jar,
		TTL:      ttl,
	}
}

// Lifetime implements session.Store.
func (s *Store) Lifetime() time.Duration { return s.TTL }

// Load implements session.Store.
func (s *Store) Load(r *http.Request) (*session.Session, error) {
	id, err := s.Jar.Get(r, session.CookieName)
	if err != nil || id == "" {
		return nil, session.ErrNotFound
	}

	ctx := r.Context()

	doc, err := s.Sessions.FindOne(ctx, rylamongo.Filter{"_id": id})
	if errors.Is(err, rylamongo.ErrNotFound) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		// The id is left out of the message on purpose: a session id in a log
		// file is a credential anyone reading the log can replay.
		return nil, fmt.Errorf("session/mongo: loading session: %w", err)
	}

	// The TTL index is a background reaper that runs about once a minute, so an
	// expired document is routinely still present. Trusting the index would mean
	// honouring a session for up to a minute after it ended.
	if time.Now().After(doc.ExpiresAt) {
		// Cleaning up as we pass keeps the collection tidy without waiting for
		// the reaper, and a failed delete only means it goes on the next sweep.
		_, _ = s.Sessions.Delete(ctx, rylamongo.Filter{"_id": id})
		return nil, session.ErrNotFound
	}

	data := doc.Data
	if data == nil {
		data = map[string]string{}
	}
	return &session.Session{ID: doc.ID, Data: data, ExpiresAt: doc.ExpiresAt}, nil
}

// Save implements session.Store.
func (s *Store) Save(w http.ResponseWriter, r *http.Request, sess *session.Session) error {
	ttl := time.Until(sess.ExpiresAt)
	if ttl <= 0 {
		ttl = s.TTL
	}

	fields := bson.M{
		"data":       sess.Data,
		"expires_at": sess.ExpiresAt.UTC(),
		"updated_at": time.Now().UTC(),
	}
	update := rylamongo.Update{"$set": fields}

	// The user id is mirrored to a top-level field because that is what makes
	// revoking a user's sessions a single indexed delete rather than a scan of
	// every session's data.
	if userID := sess.Get(session.UserIDKey); userID != "" {
		fields["user_id"] = userID
	} else {
		// Removed rather than stored empty, so an anonymous session stays out of
		// the sparse index — and so a stale id cannot survive a logout that
		// reuses the document.
		update["$unset"] = bson.M{"user_id": ""}
	}

	// An upsert, because a regenerated id is a new document and an existing one
	// is an update; login does both in the same request.
	if err := s.Sessions.Upsert(r.Context(), rylamongo.Filter{"_id": sess.ID}, update); err != nil {
		return fmt.Errorf("session/mongo: saving session: %w", err)
	}

	s.Jar.Set(w, session.CookieName, sess.ID, cookie.Options{MaxAge: int(ttl.Seconds())})
	return nil
}

// Destroy implements session.Store.
func (s *Store) Destroy(w http.ResponseWriter, r *http.Request, sess *session.Session) error {
	// The cookie goes first and unconditionally: even if the delete below fails,
	// the browser should stop presenting the id.
	s.Jar.Delete(w, session.CookieName)
	if sess == nil || sess.ID == "" {
		return nil
	}

	if _, err := s.Sessions.Delete(r.Context(), rylamongo.Filter{"_id": sess.ID}); err != nil {
		return fmt.Errorf("session/mongo: destroying session: %w", err)
	}
	return nil
}

// DestroyForUser ends every session belonging to a user. This is "log out
// everywhere", and what a password change should call.
func (s *Store) DestroyForUser(ctx context.Context, userID string) error {
	// An empty id matches every session that never had a user, so obeying it
	// would log out the whole site rather than one account.
	if userID == "" {
		return fmt.Errorf("session/mongo: destroying a user's sessions: no user id")
	}

	if _, err := s.Sessions.DeleteMany(ctx, rylamongo.Filter{"user_id": userID}); err != nil {
		return fmt.Errorf("session/mongo: destroying a user's sessions: %w", err)
	}
	return nil
}

var _ session.Store = (*Store)(nil)
