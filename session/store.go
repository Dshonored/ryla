package session

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Dshonored/ryla/cookie"
)

// ErrNotFound means no session exists for the request.
var ErrNotFound = errors.New("session: not found")

// Store persists sessions.
type Store interface {
	// Load returns the session for a request, or ErrNotFound.
	Load(r *http.Request) (*Session, error)
	// Save persists a session and writes its cookie.
	Save(w http.ResponseWriter, r *http.Request, s *Session) error
	// Destroy removes a session and clears its cookie.
	Destroy(w http.ResponseWriter, r *http.Request, s *Session) error
	// Lifetime is how long a new session lasts.
	Lifetime() time.Duration
}

// CookieStore keeps the whole session inside a signed cookie.
//
// Nothing is stored server-side, which makes it the simplest thing that works
// and the wrong choice for authentication: a session cannot be revoked, because
// the server holds no record of it. Logging out clears the browser's copy and
// nothing more. Use DBStore when sessions carry a login.
type CookieStore struct {
	Jar      *cookie.Jar
	TTL      time.Duration
	MaxBytes int
}

// NewCookieStore builds a cookie-backed store.
func NewCookieStore(jar *cookie.Jar, ttl time.Duration) *CookieStore {
	return &CookieStore{Jar: jar, TTL: ttl, MaxBytes: 3800}
}

// Lifetime implements Store.
func (s *CookieStore) Lifetime() time.Duration { return s.TTL }

type cookiePayload struct {
	ID        string            `json:"i"`
	Data      map[string]string `json:"d"`
	ExpiresAt time.Time         `json:"e"`
}

// Load implements Store.
func (s *CookieStore) Load(r *http.Request) (*Session, error) {
	raw, err := s.Jar.Get(r, CookieName)
	if err != nil {
		return nil, ErrNotFound
	}

	var p cookiePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, ErrNotFound
	}

	sess := &Session{ID: p.ID, Data: p.Data, ExpiresAt: p.ExpiresAt}
	if sess.Data == nil {
		sess.Data = map[string]string{}
	}
	if sess.Expired() {
		return nil, ErrNotFound
	}
	return sess, nil
}

// Save implements Store.
func (s *CookieStore) Save(w http.ResponseWriter, _ *http.Request, sess *Session) error {
	raw, err := json.Marshal(cookiePayload{ID: sess.ID, Data: sess.Data, ExpiresAt: sess.ExpiresAt})
	if err != nil {
		return err
	}

	// Browsers cap a cookie at roughly 4 KB and silently drop anything larger,
	// which would look like the session randomly vanishing.
	if s.MaxBytes > 0 && len(raw) > s.MaxBytes {
		return errors.New("session: too large for a cookie store; use the database store")
	}

	s.Jar.Set(w, CookieName, string(raw), cookie.Options{
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	sess.markSaved()
	return nil
}

// Destroy implements Store.
func (s *CookieStore) Destroy(w http.ResponseWriter, _ *http.Request, _ *Session) error {
	s.Jar.Delete(w, CookieName)
	return nil
}
