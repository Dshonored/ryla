// Package router wraps chi with a small, Laravel-flavoured route DSL.
//
// Handlers stay plain http.HandlerFunc and middleware stays plain
// func(http.Handler) http.Handler, so anything from the wider Go ecosystem
// drops in unchanged. The wrapper exists to provide route groups, prefixes and
// named routes — and to remove chi's requirement that middleware be declared
// before any route on the same mux.
package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
)

// Middleware is the standard net/http middleware signature.
type Middleware = func(http.Handler) http.Handler

// Router registers routes and middleware.
//
// Registrations are recorded and the underlying mux is built once, on the first
// request. That is what allows Use to be called after routes have already been
// declared: chi panics in that case, and the ordering constraint is an
// implementation detail no application should have to know about. It also means
// a route added after the server starts serving is ignored — declare
// everything during start-up.
type Router struct {
	mu     sync.Mutex
	names  *registry
	prefix string

	middleware []Middleware
	ops        []func(chi.Router)

	once  sync.Once
	built chi.Router
}

// New creates an empty Router.
func New() *Router {
	return &Router{names: newRegistry()}
}

// ServeHTTP makes Router an http.Handler, building the mux on first use.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.once.Do(func() {
		mux := chi.NewRouter()
		r.applyTo(mux)
		r.built = mux
	})
	r.built.ServeHTTP(w, req)
}

// applyTo replays this router's middleware and routes onto a chi mux, in the
// order chi requires regardless of the order they were declared in.
func (r *Router) applyTo(mux chi.Router) {
	r.mu.Lock()
	middleware := make([]Middleware, len(r.middleware))
	copy(middleware, r.middleware)

	ops := make([]func(chi.Router), len(r.ops))
	copy(ops, r.ops)
	r.mu.Unlock()

	for _, mw := range middleware {
		mux.Use(mw)
	}
	for _, op := range ops {
		op(mux)
	}
}

func (r *Router) record(op func(chi.Router)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops = append(r.ops, op)
}

// Use appends middleware. Unlike chi, it may be called at any point — before or
// after the routes it applies to.
func (r *Router) Use(mw ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, mw...)
}

// Group runs fn against an isolated router that inherits the current prefix and
// middleware, so middleware added inside fn applies only to routes declared
// there.
//
//	r.Group(func(r *router.Router) {
//	    r.Use(auth.RequireAuth("/login"))
//	    r.Get("/dashboard", h)
//	})
func (r *Router) Group(fn func(*Router)) {
	sub := &Router{names: r.names, prefix: r.prefix}
	fn(sub)

	r.record(func(c chi.Router) {
		c.Group(func(inner chi.Router) { sub.applyTo(inner) })
	})
}

// Route mounts a sub-router under prefix.
//
//	r.Route("/admin", func(r *router.Router) {
//	    r.Get("/users", h)   // -> /admin/users
//	})
func (r *Router) Route(prefix string, fn func(*Router)) {
	sub := &Router{names: r.names, prefix: joinPath(r.prefix, prefix)}
	fn(sub)

	r.record(func(c chi.Router) {
		c.Route(prefix, func(inner chi.Router) { sub.applyTo(inner) })
	})
}

// Mount attaches an arbitrary http.Handler at prefix. Routes behind a Mount
// cannot be named, since the router cannot see them.
func (r *Router) Mount(prefix string, h http.Handler) {
	r.record(func(c chi.Router) { c.Mount(prefix, h) })
}

// Method registers a handler for an arbitrary HTTP method.
func (r *Router) Method(method, pattern string, h http.HandlerFunc) *Route {
	r.record(func(c chi.Router) { c.Method(method, pattern, h) })
	return &Route{
		Method:  method,
		Pattern: joinPath(r.prefix, pattern),
		names:   r.names,
	}
}

// Get registers a GET route.
func (r *Router) Get(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodGet, pattern, h)
}

// Post registers a POST route.
func (r *Router) Post(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodPost, pattern, h)
}

// Put registers a PUT route.
func (r *Router) Put(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodPut, pattern, h)
}

// Patch registers a PATCH route.
func (r *Router) Patch(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodPatch, pattern, h)
}

// Delete registers a DELETE route.
func (r *Router) Delete(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodDelete, pattern, h)
}

// Options registers an OPTIONS route.
func (r *Router) Options(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodOptions, pattern, h)
}

// Head registers a HEAD route.
func (r *Router) Head(pattern string, h http.HandlerFunc) *Route {
	return r.Method(http.MethodHead, pattern, h)
}

// Static serves the contents of fsys under prefix. Pass an embed.FS to keep
// assets inside the single application binary.
func (r *Router) Static(prefix string, fsys fs.FS) {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	r.record(func(c chi.Router) {
		c.Handle(prefix+"*", http.StripPrefix(prefix, http.FileServerFS(fsys)))
	})
}

// NotFound sets the handler used when no route matches.
func (r *Router) NotFound(h http.HandlerFunc) {
	r.record(func(c chi.Router) { c.NotFound(h) })
}

// MethodNotAllowed sets the handler used when a path matches but the method
// does not.
func (r *Router) MethodNotAllowed(h http.HandlerFunc) {
	r.record(func(c chi.Router) { c.MethodNotAllowed(h) })
}

// URL builds the path for a named route. Params are key/value pairs matching
// the placeholders in the route pattern:
//
//	r.Get("/users/{id}", h).Name("users.show")
//	r.URL("users.show", "id", 42)   // "/users/42"
//
// It returns an error for an unknown route name, an odd number of params, or a
// placeholder left unfilled — all of which are bugs worth surfacing rather than
// papering over with a broken link.
func (r *Router) URL(name string, params ...any) (string, error) {
	return r.names.build(name, params...)
}

// MustURL is URL but panics on error. Use it where a failure means the template
// or handler is simply wrong, and route names are not user input.
func (r *Router) MustURL(name string, params ...any) string {
	u, err := r.URL(name, params...)
	if err != nil {
		panic(err)
	}
	return u
}

// Routes returns every named route, for `ry routes` and for debugging.
func (r *Router) Routes() []NamedRoute { return r.names.all() }

// Param returns a URL path parameter from the request.
func Param(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// Route is a registered route. The only reason to hold on to one is to name it.
type Route struct {
	Method  string
	Pattern string

	names *registry
}

// Name assigns a lookup name to the route and returns the route, so it chains
// off a registration call.
func (rt *Route) Name(name string) *Route {
	rt.names.add(name, rt.Method, rt.Pattern)
	return rt
}

// NamedRoute is a route that has been given a name.
type NamedRoute struct {
	Name    string
	Method  string
	Pattern string
}

func joinPath(prefix, pattern string) string {
	switch {
	case prefix == "" || prefix == "/":
		if pattern == "" {
			return "/"
		}
		return pattern
	case pattern == "" || pattern == "/":
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(pattern, "/")
}

// fillPattern substitutes {name} and {name:regexp} placeholders in a chi
// pattern with the supplied values.
func fillPattern(pattern string, values map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c != '{' {
			b.WriteByte(c)
			i++
			continue
		}

		end := matchBrace(pattern, i)
		if end < 0 {
			return "", fmt.Errorf("router: unbalanced { in pattern %q", pattern)
		}

		key := pattern[i+1 : end]
		if colon := strings.IndexByte(key, ':'); colon >= 0 {
			key = key[:colon]
		}

		v, ok := values[key]
		if !ok {
			return "", fmt.Errorf("router: missing value for {%s} in pattern %q", key, pattern)
		}
		b.WriteString(v)
		delete(values, key)
		i = end + 1
	}
	return b.String(), nil
}

// matchBrace returns the index of the '}' closing the '{' at start, accounting
// for nested braces inside a regexp constraint such as {id:[0-9]{4}}.
func matchBrace(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
