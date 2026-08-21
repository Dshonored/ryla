package middleware

import (
	"net/http"
	"strings"
)

// ForceHTTPS sends a plaintext request to the https:// form of the same URL.
//
// This site serves install.sh, and the command on the front page pipes it
// straight into a shell. Fetched over plain HTTP that is not an install script,
// it is an invitation: anyone between the visitor and here — a café router, an
// ISP, whatever is upstream of a hotel wifi — can return a different script,
// and it runs the moment it lands. The redirect is what makes the plaintext URL
// harmless rather than dangerous.
//
// The scheme this process sees is always http, because TLS is terminated by the
// proxy in front of it, so X-Forwarded-Proto is the only thing that knows what
// the browser actually used. Its presence is also what decides whether any of
// this applies: a request that arrives without it did not come through that
// proxy — `ry dev` on a laptop, a probe against the container — and redirecting
// it would send it somewhere that answers nothing.
//
// That header, and not a setting. The first version of this keyed off APP_URL,
// which the container does not set, so the whole thing was switched off in the
// one place it was written for and nothing said so. A rule that configures
// itself from the request cannot be quietly disabled by a missing variable.
func ForceHTTPS() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proto, ok := forwardedProto(r)
			if !ok {
				// Nothing in front of us, so there is no https address to
				// promise and nothing to redirect to.
				next.ServeHTTP(w, r)
				return
			}

			if !strings.EqualFold(proto, "https") && r.TLS == nil {
				// 308 rather than 301: it keeps the method and body, so a POST
				// is not quietly turned into a GET on the way through. Nothing
				// here takes a POST today, which is exactly the kind of thing
				// that changes without anyone rereading this.
				http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect)
				return
			}

			// Two years, and only ever sent over a connection that is already
			// secure. A browser that has seen this once will not try plaintext
			// again, which closes the same hole for the request after this one.
			//
			// Without includeSubDomains on purpose: it would apply to every
			// subdomain this domain ever has, including one somebody stands up
			// on plain HTTP in a hurry, and the failure then is a name that
			// simply does not load.
			w.Header().Set("Strict-Transport-Security", "max-age=63072000")
			next.ServeHTTP(w, r)
		})
	}
}

// forwardedProto reports the scheme the proxy said the browser used, and
// whether it said anything at all.
//
// A proxy may list several, oldest first — "https, http" — and the first is the
// hop that matters, because that is the one the browser made.
func forwardedProto(r *http.Request) (string, bool) {
	raw := r.Header.Get("X-Forwarded-Proto")
	if raw == "" {
		return "", false
	}

	first, _, _ := strings.Cut(raw, ",")
	first = strings.TrimSpace(first)
	if first == "" {
		return "", false
	}
	return first, true
}
