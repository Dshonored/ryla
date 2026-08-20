package bearer

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dshonored/ryla/auth"
	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/session"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// identity is the handler under test: it reports who the framework thinks is
// signed in, reading it exactly the way an application's own code does.
func identity(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = auth.ID(session.From(r.Context()))
		_, _ = w.Write([]byte("ok"))
	})
}

func authenticated(t *testing.T, s *Store, h http.Handler) http.Handler {
	t.Helper()
	return Authenticate(Config{Store: s, Logger: discard()})(h)
}

func request(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	if token != "" {
		r.Header.Set(HeaderName, Scheme+" "+token)
	}
	return r
}

// TestABearerTokenLandsWhereSessionAuthDoes is the promise the package makes to
// every handler already written against session login: the user id arrives in
// the same place, so nothing downstream has to know which credential was used.
func TestABearerTokenLandsWhereSessionAuthDoes(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	var seen string
	rec := httptest.NewRecorder()
	authenticated(t, s, identity(&seen)).ServeHTTP(rec, request(made.Plaintext))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen != "42" {
		t.Errorf("auth.ID = %q, want 42; a token-authenticated request is not reaching the session", seen)
	}
}

// TestTheSchemeIsMatchedCaseInsensitively follows RFC 6750, and more
// practically the HTTP clients that send "bearer".
func TestTheSchemeIsMatchedCaseInsensitively(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	var seen string
	r := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	r.Header.Set(HeaderName, "bearer "+made.Plaintext)
	authenticated(t, s, identity(&seen)).ServeHTTP(httptest.NewRecorder(), r)

	if seen != "42" {
		t.Errorf("auth.ID = %q, want 42", seen)
	}
}

// TestAWrongTokenIsRejected checks that the handler never runs. A rejected
// credential that still reached the handler would leave every route relying on
// its own check.
func TestAWrongTokenIsRejected(t *testing.T) {
	s := store(t)
	create(t, s, "42", Options{})

	ran := false
	h := authenticated(t, s, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(Prefix+"never-issued"))

	if ran {
		t.Error("the handler ran despite an invalid token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	// Without this header the response is not a valid 401, and clients that
	// retry with a refreshed credential have nothing to read.
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, Scheme) {
		t.Errorf("WWW-Authenticate = %q", got)
	}
}

// TestAnExpiredTokenIsRejectedAndSaysSo distinguishes the two failures for the
// caller. They already hold the token, so naming the reason gives away nothing
// and saves them hunting for a fault that is only a clock.
func TestAnExpiredTokenIsRejectedAndSaysSo(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{ExpiresIn: time.Hour})

	past := time.Now().Add(-time.Minute)
	if err := s.DB.Model(&Token{}).Where("id = ?", made.Token.ID).
		UpdateColumn("expires_at", past).Error; err != nil {
		t.Fatalf("expire the token: %v", err)
	}

	var seen string
	rec := httptest.NewRecorder()
	authenticated(t, s, identity(&seen)).ServeHTTP(rec, request(made.Plaintext))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if seen != "" {
		t.Errorf("an expired token authenticated the request as %q", seen)
	}
	if !strings.Contains(rec.Body.String(), "expired") {
		t.Errorf("body = %q, want it to name the expiry", rec.Body.String())
	}
}

// TestARequestWithNoCredentialIsLeftAlone keeps a route usable by both a
// browser and an API client: the session established upstream must survive.
func TestARequestWithNoCredentialIsLeftAlone(t *testing.T) {
	s := store(t)

	var seen string
	sessions := session.NewCookieStore(cookie.New("test-key", false), time.Hour)

	// Sign in on one request, exactly as the login form would...
	login := httptest.NewRecorder()
	session.Middleware(sessions, discard())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Login(session.From(r.Context()), "7")
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))

	// ...then reach an API route carrying only the cookie.
	r := request("")
	for _, c := range login.Result().Cookies() {
		r.AddCookie(c)
	}
	session.Middleware(sessions, discard())(authenticated(t, s, identity(&seen))).
		ServeHTTP(httptest.NewRecorder(), r)

	if seen != "7" {
		t.Errorf("auth.ID = %q, want 7; the cookie session was lost", seen)
	}
}

// TestABadTokenDoesNotFallBackToTheSession is what keeps csrf.Protect's /api/
// exemption honest. A request that presents a token means to be authenticated
// by it; quietly serving it from an ambient cookie instead would authenticate a
// CSRF-exempt route with exactly the credential that exemption assumes is not
// in play.
func TestABadTokenDoesNotFallBackToTheSession(t *testing.T) {
	s := store(t)

	sessions := session.NewCookieStore(cookie.New("test-key", false), time.Hour)
	login := httptest.NewRecorder()
	session.Middleware(sessions, discard())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Login(session.From(r.Context()), "7")
	})).ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))

	var seen string
	r := request(Prefix + "revoked-yesterday")
	for _, c := range login.Result().Cookies() {
		r.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	session.Middleware(sessions, discard())(authenticated(t, s, identity(&seen))).ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if seen != "" {
		t.Errorf("a bad token was served as session user %q", seen)
	}
}

// TestNoSessionCookieIsIssuedForABearerRequest keeps the two credentials from
// blurring: handing an API client a session cookie would give it the ambient
// credential the CSRF exemption relies on not existing.
func TestNoSessionCookieIsIssuedForABearerRequest(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{})

	var seen string
	rec := httptest.NewRecorder()
	authenticated(t, s, identity(&seen)).ServeHTTP(rec, request(made.Plaintext))

	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("a bearer request was answered with %d cookies, first %q", len(cookies), cookies[0].Name)
	}
}

// TestScopesReachTheHandler covers the narrowing end to end, and pins the rule
// that a request with no token answers no: a scope check on a route that
// accepted an unscoped credential would widen access rather than restrict it.
func TestScopesReachTheHandler(t *testing.T) {
	s := store(t)
	made := create(t, s, "42", Options{Scopes: []string{"posts:read"}})

	var granted, refused, anonymous bool
	h := authenticated(t, s, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		granted = Can(r.Context(), "posts:read")
		refused = Can(r.Context(), "billing:write")
		if From(r.Context()) == nil {
			anonymous = true
		}
	}))

	h.ServeHTTP(httptest.NewRecorder(), request(made.Plaintext))
	if !granted || refused || anonymous {
		t.Errorf("granted = %v, refused = %v, anonymous = %v", granted, refused, anonymous)
	}

	if Can(context.Background(), "posts:read") {
		t.Error("a request with no token passed a scope check")
	}
}

// TestAuthenticateRefusesToRunWithoutAStore fails at wiring time rather than
// letting every API request through unauthenticated at three in the morning.
func TestAuthenticateRefusesToRunWithoutAStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Authenticate accepted a nil store")
		}
	}()
	Authenticate(Config{})
}
