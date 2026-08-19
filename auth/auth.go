package auth

import (
	"net/http"
	"strconv"

	"github.com/Dshonored/ryla/session"
)

// UserIDKey is the session key holding the authenticated user's id.
const UserIDKey = session.UserIDKey

// Login marks the session as belonging to a user.
//
// The session id is regenerated first. Without that, an id an attacker planted
// in the victim's browser before login stays valid afterwards, and they inherit
// the authenticated session — session fixation. It costs one line and closes
// the hole entirely.
func Login(s *session.Session, userID string) {
	s.Regenerate()
	s.Put(UserIDKey, userID)
}

// LoginID is Login for a numeric primary key.
func LoginID(s *session.Session, userID uint) {
	Login(s, strconv.FormatUint(uint64(userID), 10))
}

// Logout clears the session and issues a new id.
//
// The session survives so that a flash message can be shown on the page the
// user lands on; it simply no longer identifies anyone.
func Logout(s *session.Session) { s.Invalidate() }

// ID returns the authenticated user's id, or "".
func ID(s *session.Session) string { return s.Get(UserIDKey) }

// UintID returns the authenticated user's id as a uint, and whether one is set.
func UintID(s *session.Session) (uint, bool) {
	raw := ID(s)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(n), true
}

// Check reports whether the session is authenticated.
func Check(s *session.Session) bool { return ID(s) != "" }

// CheckRequest reports whether a request is authenticated.
func CheckRequest(r *http.Request) bool { return Check(session.From(r.Context())) }

// IntendedKey holds the URL a guest was trying to reach before being sent to
// the login page.
const IntendedKey = "auth_intended"

// RequireAuth sends unauthenticated visitors to loginPath.
//
// The URL they were trying to reach is remembered, so logging in returns them
// to it rather than dumping them on a dashboard.
func RequireAuth(loginPath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := session.From(r.Context())
			if Check(s) {
				next.ServeHTTP(w, r)
				return
			}

			// Only worth remembering a page they could come back to.
			if r.Method == http.MethodGet {
				s.Put(IntendedKey, r.URL.RequestURI())
			}
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
		})
	}
}

// RequireGuest sends authenticated visitors away from pages meant for guests,
// such as login and register.
func RequireGuest(homePath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Check(session.From(r.Context())) {
				http.Redirect(w, r, homePath, http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Intended returns where to send a user after login, clearing the stored value.
// It falls back to fallback when there is nothing remembered.
func Intended(s *session.Session, fallback string) string {
	if target := s.Pull(IntendedKey); target != "" {
		return target
	}
	return fallback
}
