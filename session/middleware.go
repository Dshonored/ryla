package session

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

type ctxKey int

const sessionKey ctxKey = 0

// Middleware loads the session for each request and saves it afterwards.
//
// Saving happens on the first write rather than after the handler returns,
// because the session cookie is a header and headers cannot be set once the
// response body has started. A handler that redirects — which every successful
// login does — writes its header immediately, so anything saved later would be
// silently dropped.
func Middleware(store Store, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, err := store.Load(r)
			switch {
			case errors.Is(err, ErrNotFound):
				sess = New(store.Lifetime())
			case err != nil:
				// A store failure should not take the page down; the visitor
				// gets a fresh session and the problem is logged.
				log.Error("could not load session", "error", err)
				sess = New(store.Lifetime())
			default:
				sess.Touch(store.Lifetime())
			}

			sw := &saver{
				ResponseWriter: w,
				req:            r,
				store:          store,
				session:        sess,
				log:            log,
			}

			next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))

			// A handler that wrote nothing at all still needs its session
			// persisted — a login that only sets a cookie, for instance.
			sw.save()
		})
	}
}

// From returns the session attached to a request context.
//
// It never returns nil: a handler running without the middleware gets a
// detached session that behaves normally and is simply never persisted, which
// is far easier to debug than a nil dereference.
func From(ctx context.Context) *Session {
	if s, ok := ctx.Value(sessionKey).(*Session); ok {
		return s
	}
	return New(0)
}

// saver flushes the session before the response headers are committed.
type saver struct {
	http.ResponseWriter

	req     *http.Request
	store   Store
	session *Session
	log     *slog.Logger
	saved   bool
}

func (s *saver) save() {
	if s.saved {
		return
	}
	s.saved = true

	// An unchanged session costs nothing: no write, no cookie refresh.
	if !s.session.Dirty() {
		return
	}
	if err := s.store.Save(s.ResponseWriter, s.req, s.session); err != nil {
		s.log.Error("could not save session", "error", err)
	}
}

func (s *saver) WriteHeader(status int) {
	s.save()
	s.ResponseWriter.WriteHeader(status)
}

func (s *saver) Write(b []byte) (int, error) {
	s.save()
	return s.ResponseWriter.Write(b)
}

// Unwrap keeps http.ResponseController working for flushing and hijacking.
func (s *saver) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Destroy ends the session behind a request. Handlers should prefer
// Session.Invalidate, which keeps a usable session for the flash message that
// usually follows a logout.
func Destroy(w http.ResponseWriter, r *http.Request, store Store) error {
	return store.Destroy(w, r, From(r.Context()))
}
