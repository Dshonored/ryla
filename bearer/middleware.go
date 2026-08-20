package bearer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Dshonored/ryla/session"
)

const (
	// HeaderName carries the credential.
	HeaderName = "Authorization"
	// Scheme is the authentication scheme, matched case-insensitively as
	// RFC 6750 requires.
	Scheme = "Bearer"
)

type ctxKey int

const (
	tokenKey ctxKey = iota
	errorKey
)

// Config tunes the middleware.
type Config struct {
	// Store resolves tokens. Required.
	Store *Store
	// Logger records failures that are the server's fault rather than the
	// caller's. Defaults to slog.Default.
	Logger *slog.Logger
	// ErrorHandler renders a rejected credential. The default is a 401 with a
	// WWW-Authenticate header and a small JSON body; call Error to read why.
	ErrorHandler http.Handler
}

// Authenticate returns middleware that authenticates a request from its
// Authorization: Bearer header.
//
// A request carrying no bearer credential passes through untouched, so a route
// can serve both a browser session and an API client. A request carrying a bad
// one is rejected outright rather than continuing as a guest: the caller meant
// to authenticate, and quietly serving them anonymous data hides a revoked
// token from whoever is still using it.
func Authenticate(cfg Config) func(http.Handler) http.Handler {
	if cfg.Store == nil {
		panic("bearer: Config.Store is required")
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	deny := cfg.ErrorHandler
	if deny == nil {
		deny = http.HandlerFunc(denyRequest)
	}

	return func(next http.Handler) http.Handler {
		// Built once, not per request: the bridge holds no state and the
		// wrapped handler never changes.
		authenticated := session.Middleware(bridge{}, log)(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := credential(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			tok, err := cfg.Store.Verify(r.Context(), raw)
			if err != nil {
				if !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrExpired) {
					// The database is down or the query failed. That is not the
					// caller's doing, and it must be visible in the logs rather
					// than looking like a wave of bad tokens.
					log.Error("could not verify bearer token", "error", err)
				}
				deny.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), errorKey, err)))
				return
			}

			authenticated.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, tok)))
		})
	}
}

// From returns the token that authenticated this request, or nil when it was
// authenticated by a session or not at all.
func From(ctx context.Context) *Token {
	t, _ := ctx.Value(tokenKey).(*Token)
	return t
}

// Error returns why a credential was rejected, for a custom ErrorHandler.
func Error(ctx context.Context) error {
	err, _ := ctx.Value(errorKey).(error)
	return err
}

// Can reports whether the token authenticating this request carries scope.
//
// It is false when there is no token. A scope is a restriction on one
// credential, so answering true for a request that presented none would widen
// access on exactly the routes that bothered to ask — a cookie session is
// unscoped, and a route that cares about scopes should not accept one.
func Can(ctx context.Context, scope string) bool {
	t := From(ctx)
	return t != nil && t.Can(scope)
}

// credential extracts the token from the Authorization header.
func credential(r *http.Request) (string, bool) {
	scheme, value, found := strings.Cut(r.Header.Get(HeaderName), " ")
	if !found || !strings.EqualFold(scheme, Scheme) {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// denyRequest is the default rejection: a 401 shaped for an API client.
//
// WWW-Authenticate is not decoration. Without it the response is not a valid
// 401, and HTTP clients that retry with a refreshed credential rely on reading
// the scheme back.
func denyRequest(w http.ResponseWriter, r *http.Request) {
	description := "The access token is invalid."
	if errors.Is(Error(r.Context()), ErrExpired) {
		description = "The access token has expired."
	}

	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="`+description+`"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token","message":"` + description + `"}`))
}

// bridge puts the authenticated user id where session.From will find it.
//
// The context key session uses is unexported and session.Middleware is the only
// thing that writes it, so the honest way to reach it is to be a session.Store.
// The alternative — a second context key of this package's own — would give
// downstream code two different places to look for the same fact, which is
// precisely what this package promises not to create.
type bridge struct{}

// Load implements session.Store, building a session from the verified token.
func (bridge) Load(r *http.Request) (*session.Session, error) {
	tok := From(r.Context())
	if tok == nil {
		return nil, session.ErrNotFound
	}

	s := session.New(0)
	s.Put(session.UserIDKey, tok.UserID)
	return s, nil
}

// Save implements session.Store and deliberately does nothing. A bearer request
// carries its whole identity in the header; there is no state to persist and no
// cookie to set, and writing one would hand a browser an ambient credential the
// CSRF exemption assumes does not exist.
func (bridge) Save(http.ResponseWriter, *http.Request, *session.Session) error { return nil }

// Destroy implements session.Store. Ending a bearer identity means revoking the
// token, which Store.Revoke does; there is nothing per-request to tear down.
func (bridge) Destroy(http.ResponseWriter, *http.Request, *session.Session) error { return nil }

// Lifetime implements session.Store. The token's own expiry governs how long
// the identity lasts, so there is no separate session lifetime to extend.
func (bridge) Lifetime() time.Duration { return 0 }
