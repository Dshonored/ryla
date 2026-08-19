package auth

import (
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/session"
)

func store() session.Store {
	return session.NewCookieStore(cookie.New("test-key", false), time.Hour)
}

func serve(store session.Store, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	session.Middleware(store, slog.New(slog.NewTextHandler(io.Discard, nil)))(h).ServeHTTP(rec, r)
	return rec
}

func withCookies(rec *httptest.ResponseRecorder, method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			r.AddCookie(c)
		}
	}
	return r
}

// TestLoginRegeneratesTheSessionID closes session fixation: an id an attacker
// planted in the victim's browser before login must not still be valid after
// it, or they inherit the authenticated session.
func TestLoginRegeneratesTheSessionID(t *testing.T) {
	s := session.New(time.Hour)
	before := s.ID

	Login(s, "42")

	if s.ID == before {
		t.Fatal("the session id was not regenerated on login")
	}
	if ID(s) != "42" {
		t.Errorf("ID = %q, want 42", ID(s))
	}
	if !Check(s) {
		t.Error("Check = false after login")
	}
}

func TestLogoutClearsIdentity(t *testing.T) {
	s := session.New(time.Hour)
	Login(s, "42")
	s.Put("cart", "3 items")

	Logout(s)

	if Check(s) {
		t.Error("still authenticated after logout")
	}
	if s.Get("cart") != "" {
		t.Error("session data survived logout")
	}
}

func TestUintID(t *testing.T) {
	s := session.New(time.Hour)

	if _, ok := UintID(s); ok {
		t.Error("an anonymous session reported a user id")
	}

	LoginID(s, 7)
	got, ok := UintID(s)
	if !ok || got != 7 {
		t.Errorf("UintID = (%d, %v), want (7, true)", got, ok)
	}
}

func TestRequireAuthRedirectsGuests(t *testing.T) {
	protected := RequireAuth("/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))

	rec := serve(store(), protected, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Errorf("Location = %q", got)
	}
	if rec.Body.String() == "secret" {
		t.Error("the protected handler ran anyway")
	}
}

func TestRequireAuthAllowsAuthenticated(t *testing.T) {
	st := store()

	// Log in on one request...
	login := serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Login(session.From(r.Context()), "42")
	}), httptest.NewRequest(http.MethodPost, "/login", nil))

	// ...then reach the protected page on the next.
	protected := RequireAuth("/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	rec := serve(st, protected, withCookies(login, http.MethodGet, "/dashboard"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "secret" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestIntendedURLIsRemembered is the difference between logging in and landing
// where you were going, versus logging in and landing on a dashboard you did
// not ask for.
func TestIntendedURLIsRemembered(t *testing.T) {
	st := store()

	protected := RequireAuth("/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	redirected := serve(st, protected, httptest.NewRequest(http.MethodGet, "/reports?year=2026", nil))

	var target string
	serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target = Intended(session.From(r.Context()), "/")
	}), withCookies(redirected, http.MethodGet, "/login"))

	if target != "/reports?year=2026" {
		t.Errorf("Intended = %q, want the original URL including its query", target)
	}
}

func TestIntendedFallsBackAndClears(t *testing.T) {
	s := session.New(time.Hour)

	if got := Intended(s, "/home"); got != "/home" {
		t.Errorf("Intended = %q, want the fallback", got)
	}

	s.Put(IntendedKey, "/reports")
	if got := Intended(s, "/home"); got != "/reports" {
		t.Errorf("Intended = %q", got)
	}
	// Reading consumes it, so a later login does not bounce somewhere stale.
	if got := Intended(s, "/home"); got != "/home" {
		t.Errorf("Intended was not cleared after being read: %q", got)
	}
}

func TestOnlyGETIsRemembered(t *testing.T) {
	st := store()

	protected := RequireAuth("/login")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := serve(st, protected, httptest.NewRequest(http.MethodPost, "/reports", nil))

	var target string
	serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target = Intended(session.From(r.Context()), "/")
	}), withCookies(rec, http.MethodGet, "/login"))

	// Redirecting to a POST target with a GET would just 405; there is nothing
	// useful to return to.
	if target != "/" {
		t.Errorf("Intended = %q, want the fallback for a POST", target)
	}
}

func TestRequireGuestRedirectsAuthenticated(t *testing.T) {
	st := store()

	login := serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Login(session.From(r.Context()), "42")
	}), httptest.NewRequest(http.MethodPost, "/login", nil))

	guestOnly := RequireGuest("/")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("login form"))
	}))
	rec := serve(st, guestOnly, withCookies(login, http.MethodGet, "/login"))

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rec.Code)
	}
}

func TestCheckRequest(t *testing.T) {
	st := store()

	login := serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Login(session.From(r.Context()), "42")
	}), httptest.NewRequest(http.MethodPost, "/login", nil))

	var authenticated bool
	serve(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated = CheckRequest(r)
	}), withCookies(login, http.MethodGet, "/"))

	if !authenticated {
		t.Error("CheckRequest = false for an authenticated request")
	}
}

func TestTokenRoundTrip(t *testing.T) {
	s := NewSigner("test-key")

	token := s.Sign(PurposeVerifyEmail, "42", time.Hour)
	subject, err := s.Verify(PurposeVerifyEmail, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if subject != "42" {
		t.Errorf("subject = %q, want 42", subject)
	}
}

// TestTokenIsBoundToItsPurpose is why purpose is part of the signature: a link
// that verifies an email address must not be replayable to reset a password.
func TestTokenIsBoundToItsPurpose(t *testing.T) {
	s := NewSigner("test-key")
	token := s.Sign(PurposeVerifyEmail, "42", time.Hour)

	if _, err := s.Verify(PurposeResetPassword, token); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a verification token was accepted for a password reset: %v", err)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	s := NewSigner("test-key")
	token := s.Sign(PurposeVerifyEmail, "42", -time.Second)

	if _, err := s.Verify(PurposeVerifyEmail, token); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("Verify = %v, want ErrTokenExpired", err)
	}
}

func TestTamperedTokenIsRejected(t *testing.T) {
	s := NewSigner("test-key")
	token := s.Sign(PurposeVerifyEmail, "42", time.Hour)

	// Swap the subject for another user's id, keeping the signature.
	_, rest, _ := strings.Cut(token, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte("1")) + "." + rest

	if _, err := s.Verify(PurposeVerifyEmail, forged); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("a token pointing at another user was accepted: %v", err)
	}
}

func TestTokenFromAnotherKeyIsRejected(t *testing.T) {
	token := NewSigner("key-one").Sign(PurposeVerifyEmail, "42", time.Hour)

	if _, err := NewSigner("key-two").Verify(PurposeVerifyEmail, token); err == nil {
		t.Error("a token signed with a different key was accepted")
	}
}

func TestMalformedTokens(t *testing.T) {
	s := NewSigner("test-key")

	for _, token := range []string{"", "nodots", "one.two", "!!!.123.abc", "a.notanumber.c"} {
		if _, err := s.Verify(PurposeVerifyEmail, token); err == nil {
			t.Errorf("malformed token %q was accepted", token)
		}
	}
}
