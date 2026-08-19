// Package session stores per-visitor state across requests.
//
// A session id lives in a signed cookie; where the data lives depends on the
// Store. The cookie store keeps everything in the cookie itself and needs no
// infrastructure; the database store keeps only the id there, which is what
// allows a session to be revoked server-side — the thing "log out everywhere"
// and "log out on password change" both depend on.
package session

import (
	"crypto/rand"
	"encoding/base64"
	"strconv"
	"time"
)

// CookieName holds the session.
const CookieName = "ryla_session"

// Session is one visitor's state.
//
// Values are strings rather than any: a session round-trips through JSON, and
// map[string]any would quietly turn every integer into a float on the way back.
// The typed helpers below convert explicitly instead.
type Session struct {
	ID        string
	Data      map[string]string
	ExpiresAt time.Time

	// dirty tracks whether anything changed, so an unmodified session does not
	// cost a database write on every request.
	dirty bool
	// fresh marks a session that has never been persisted.
	fresh bool
}

// New creates an empty session.
//
// It starts clean, not dirty. A visitor who never has anything stored for them
// — most crawlers, and anyone who only reads a public page — must not cost a
// database row, or the sessions table fills with sessions nobody will ever use
// again. Only an actual write marks it for saving.
func New(lifetime time.Duration) *Session {
	return &Session{
		ID:        NewID(),
		Data:      map[string]string{},
		ExpiresAt: time.Now().Add(lifetime),
		fresh:     true,
	}
}

// NewID returns a random session identifier.
func NewID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// No entropy source means any id we invent is guessable, and a
		// guessable session id is account takeover. Refusing to serve is the
		// only safe answer.
		panic("session: could not read random bytes: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Get returns a value, or "" when absent.
func (s *Session) Get(key string) string { return s.Data[key] }

// Has reports whether a key is set.
func (s *Session) Has(key string) bool {
	_, ok := s.Data[key]
	return ok
}

// Put stores a value.
func (s *Session) Put(key, value string) {
	if s.Data == nil {
		s.Data = map[string]string{}
	}
	if existing, ok := s.Data[key]; ok && existing == value {
		return
	}
	s.Data[key] = value
	s.dirty = true
}

// Pull returns a value and removes it.
func (s *Session) Pull(key string) string {
	v := s.Data[key]
	s.Forget(key)
	return v
}

// Forget removes a key.
func (s *Session) Forget(key string) {
	if _, ok := s.Data[key]; !ok {
		return
	}
	delete(s.Data, key)
	s.dirty = true
}

// Clear removes everything, keeping the same id.
func (s *Session) Clear() {
	if len(s.Data) == 0 {
		return
	}
	s.Data = map[string]string{}
	s.dirty = true
}

// GetInt returns a value parsed as an int, or def.
func (s *Session) GetInt(key string, def int) int {
	n, err := strconv.Atoi(s.Data[key])
	if err != nil {
		return def
	}
	return n
}

// PutInt stores an integer.
func (s *Session) PutInt(key string, value int) { s.Put(key, strconv.Itoa(value)) }

// GetBool returns a value parsed as a bool.
func (s *Session) GetBool(key string) bool {
	b, err := strconv.ParseBool(s.Data[key])
	return err == nil && b
}

// PutBool stores a boolean.
func (s *Session) PutBool(key string, value bool) { s.Put(key, strconv.FormatBool(value)) }

// Regenerate issues a new id while keeping the data.
//
// Call it whenever privileges change — logging in above all. Without it, an id
// an attacker planted in the victim's browser before login stays valid
// afterwards, which is session fixation.
func (s *Session) Regenerate() {
	s.ID = NewID()
	s.fresh = true
	s.dirty = true
}

// Invalidate discards the data and issues a new id. This is what logout does.
func (s *Session) Invalidate() {
	s.Data = map[string]string{}
	s.Regenerate()
}

// Touch extends the expiry.
func (s *Session) Touch(lifetime time.Duration) {
	next := time.Now().Add(lifetime)
	// Only mark dirty for a meaningful extension, or a busy session writes to
	// the store on every single request.
	if next.Sub(s.ExpiresAt) > time.Minute {
		s.ExpiresAt = next
		s.dirty = true
	}
}

// Expired reports whether the session has passed its expiry.
func (s *Session) Expired() bool { return time.Now().After(s.ExpiresAt) }

// Dirty reports whether the session needs saving.
func (s *Session) Dirty() bool { return s.dirty }

// Fresh reports whether the session has never been persisted.
func (s *Session) Fresh() bool { return s.fresh }

// markSaved is called by a Store after a successful write.
func (s *Session) markSaved() {
	s.dirty = false
	s.fresh = false
}
