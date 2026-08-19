// Package csrf protects state-changing requests from cross-site forgery.
//
// It uses the double-submit pattern: a signed token is stored in a cookie and
// the same value must arrive in the form body or a header. An attacker's page
// can cause a browser to send the cookie, but same-origin policy stops it from
// reading the cookie to echo the value back, so the two never match.
//
// The cookie is signed with the application key, which means a token cannot be
// invented — only replayed from a real session, which same-origin policy
// already prevents.
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/Dshonored/ryla/cookie"
)

const (
	// CookieName holds the token.
	CookieName = "ryla_csrf"
	// FieldName is the hidden form input carrying the token.
	FieldName = "_csrf"
	// HeaderName carries the token on fetch/XHR requests.
	HeaderName = "X-CSRF-Token"
)

type ctxKey int

const tokenKey ctxKey = 0

// Config tunes the middleware.
type Config struct {
	// Jar signs the token cookie. Required.
	Jar *cookie.Jar
	// Skip reports whether a request should bypass the check. Use it for
	// endpoints authenticated by a bearer token, which are not reachable by a
	// browser's ambient credentials and so are not vulnerable to forgery.
	Skip func(*http.Request) bool
	// ErrorHandler renders the rejection. The default is a plain 403.
	ErrorHandler http.Handler
}

// Protect returns middleware enforcing CSRF on unsafe methods.
//
// GET, HEAD and OPTIONS are exempt because they are not supposed to change
// anything. A handler that mutates state on GET is not made safe by a token —
// it needs to stop doing that.
func Protect(cfg Config) func(http.Handler) http.Handler {
	if cfg.Jar == nil {
		panic("csrf: Config.Jar is required")
	}

	deny := cfg.ErrorHandler
	if deny == nil {
		deny = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Invalid or missing CSRF token", http.StatusForbidden)
		})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Reuse the token already in the cookie so that opening a second
			// tab does not invalidate the form in the first.
			token, err := cfg.Jar.Get(r, CookieName)
			if err != nil || token == "" {
				token = newToken()
				readable := false
				cfg.Jar.Set(w, CookieName, token, cookie.Options{HTTPOnly: &readable})
			}

			ctx := context.WithValue(r.Context(), tokenKey, token)
			r = r.WithContext(ctx)

			if safe(r.Method) || (cfg.Skip != nil && cfg.Skip(r)) {
				next.ServeHTTP(w, r)
				return
			}

			if !valid(r, token) {
				deny.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Token returns the token for this request, for embedding in a form.
func Token(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey).(string)
	return t
}

// Field returns a ready-made hidden input carrying the token.
func Field(ctx context.Context) string {
	return `<input type="hidden" name="` + FieldName + `" value="` + Token(ctx) + `">`
}

func safe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// valid compares the submitted token with the cookie's.
func valid(r *http.Request, expected string) bool {
	submitted := r.Header.Get(HeaderName)
	if submitted == "" {
		// Reading the form here is safe: ParseForm caches its result, so a
		// handler calling it again sees the same values.
		if err := r.ParseForm(); err == nil {
			submitted = r.PostFormValue(FieldName)
		}
	}
	if submitted == "" {
		return false
	}
	// Constant time, so a wrong token cannot be refined guess by guess from
	// timing differences.
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the system has no entropy source. Carrying
		// on with a predictable token would be worse than refusing to serve.
		panic("csrf: could not read random bytes: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
