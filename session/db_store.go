package session

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/cookie"
)

// Record is the sessions table row.
type Record struct {
	ID        string    `gorm:"column:id;primaryKey;size:64"`
	Data      string    `gorm:"column:data;type:text"`
	UserID    string    `gorm:"column:user_id;index;size:64"`
	IP        string    `gorm:"column:ip;size:45"`
	UserAgent string    `gorm:"column:user_agent;size:255"`
	ExpiresAt time.Time `gorm:"column:expires_at;index"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName pins the table name.
func (Record) TableName() string { return "sessions" }

// UserIDKey is the session key holding the authenticated user's id. DBStore
// mirrors it onto the row so sessions can be listed or revoked per user.
const UserIDKey = "auth_user_id"

// DBStore keeps session data in the database and only the id in the cookie.
//
// This is what makes a session revocable: deleting the row ends it immediately,
// however many browsers hold the cookie. Authentication needs that; a cookie
// store cannot provide it.
type DBStore struct {
	DB  *gorm.DB
	Jar *cookie.Jar
	TTL time.Duration
}

// NewDBStore builds a database-backed store.
func NewDBStore(db *gorm.DB, jar *cookie.Jar, ttl time.Duration) *DBStore {
	return &DBStore{DB: db, Jar: jar, TTL: ttl}
}

// Lifetime implements Store.
func (s *DBStore) Lifetime() time.Duration { return s.TTL }

// Load implements Store.
func (s *DBStore) Load(r *http.Request) (*Session, error) {
	id, err := s.Jar.Get(r, CookieName)
	if err != nil || id == "" {
		return nil, ErrNotFound
	}

	var rec Record
	if err := s.DB.WithContext(r.Context()).First(&rec, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if time.Now().After(rec.ExpiresAt) {
		// Clean up as we go, so expired rows do not need a separate sweep to
		// stay under control.
		_ = s.DB.WithContext(r.Context()).Delete(&Record{}, "id = ?", id).Error
		return nil, ErrNotFound
	}

	data := map[string]string{}
	if rec.Data != "" {
		if err := json.Unmarshal([]byte(rec.Data), &data); err != nil {
			return nil, ErrNotFound
		}
	}
	return &Session{ID: rec.ID, Data: data, ExpiresAt: rec.ExpiresAt}, nil
}

// Save implements Store.
func (s *DBStore) Save(w http.ResponseWriter, r *http.Request, sess *Session) error {
	raw, err := json.Marshal(sess.Data)
	if err != nil {
		return err
	}

	rec := Record{
		ID:        sess.ID,
		Data:      string(raw),
		UserID:    sess.Get(UserIDKey),
		IP:        clientIP(r),
		UserAgent: truncate(r.UserAgent(), 255),
		ExpiresAt: sess.ExpiresAt,
		UpdatedAt: time.Now(),
	}

	// Upsert: a regenerated id is a new row, an existing one is an update.
	if err := s.DB.WithContext(r.Context()).Save(&rec).Error; err != nil {
		return err
	}

	s.Jar.Set(w, CookieName, sess.ID, cookie.Options{
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	sess.markSaved()
	return nil
}

// Destroy implements Store.
func (s *DBStore) Destroy(w http.ResponseWriter, r *http.Request, sess *Session) error {
	s.Jar.Delete(w, CookieName)
	if sess == nil || sess.ID == "" {
		return nil
	}
	return s.DB.WithContext(r.Context()).Delete(&Record{}, "id = ?", sess.ID).Error
}

// DestroyForUser ends every session belonging to a user. This is "log out
// everywhere", and what a password change should call.
func (s *DBStore) DestroyForUser(userID string) error {
	return s.DB.Delete(&Record{}, "user_id = ?", userID).Error
}

// Prune deletes expired rows. Load already removes them as it finds them, so
// this only matters for sessions nobody comes back to.
func (s *DBStore) Prune() (int64, error) {
	res := s.DB.Delete(&Record{}, "expires_at < ?", time.Now())
	return res.RowsAffected, res.Error
}

// clientIP prefers the address a proxy reported, falling back to the socket.
//
// X-Forwarded-For is trivially spoofed by a direct client, so this is only
// worth anything behind a proxy that overwrites it. It is recorded for the
// user's benefit — "which devices are signed in" — never for access control.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// The list is client, proxy1, proxy2...; the first entry is the one
		// that matters.
		first, _, _ := strings.Cut(fwd, ",")
		return truncate(strings.TrimSpace(first), 45)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return truncate(host, 45)
	}
	return truncate(r.RemoteAddr, 45)
}

// truncate keeps a value within its column width.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
