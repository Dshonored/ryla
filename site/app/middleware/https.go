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
// proxy in front of it. X-Forwarded-Proto is what that proxy sets to say what
// the browser actually used, and trusting it is only safe because nothing
// reaches this handler except through that proxy — which is why the whole thing
// is switched off unless the site is configured to be served over https at all.
func ForceHTTPS(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			if !secure(r) {
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

// secure reports whether the browser's own connection was over TLS.
func secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}

	// A proxy may list several, oldest first: "https, http".
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
