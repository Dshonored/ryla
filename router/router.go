// Package router wraps chi with a small, Laravel-flavoured route DSL.
//
// Handlers stay plain http.HandlerFunc and middleware stays plain
// func(http.Handler) http.Handler, so anything from the wider Go ecosystem
// drops in unchanged. The wrapper exists only to provide route groups,
// prefixes and named routes.
package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Middleware is the standard net/http middleware signature.
type Middleware = func(http.Handler) http.Handler

// Router registers routes and middleware. Sub-routers created by Group and
// Route share the parent's name registry, so a named route is resolvable from
// anywhere in the application.
type Router struct {
	mux    chi.Router
	names  *registry
	prefix string
}

// New creates an empty Router.
func New() *Router {
	return &Router{mux: chi.NewRouter(), names: newRegistry()}
}

// ServeHTTP makes Router an http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Use appends middleware to this router. It must be called before any route is
// registered on the same router; chi panics otherwise. To apply middleware to a
// subset of routes, use Group.
func (r *Router) Use(mw ...Middleware) {
	r.mux.Use(mw...)
}

// Group runs fn against an isolated router that inherits the current prefix and
// middleware, so middleware added inside fn applies only to routes declared
// there.
//
//	r.Group(func(r *router.Router) {
//	    r.Use(middleware.Auth)
//	    r.Get("/dashboard", h)
//	})
func (r *Router) Group(fn func(*Router)) {
	r.mux.Group(func(c chi.Router) {
		fn(&Router{mux: c, names: r.names, prefix: r.prefix})
	})
}

// Route mounts a sub-router under prefix.
//
//	r.Route("/admin", func(r *router.Router) {
//	    r.Get("/users", h)   // -> /admin/users
//	})
func (r *Router) Route(prefix string, fn func(*Router)) {
	r.mux.Route(prefix, func(c chi.Router) {
		fn(&Router{mux: c, names: r.names, prefix: joinPath(r.prefix, prefix)})
	})
}

// Mount attaches an arbitrary http.Handler at prefix. Routes behind a Mount
// cannot be named, since the router cannot see them.
func (r *Router) Mount(prefix string, h http.Handler) {
	r.mux.Mount(prefix, h)
}

// Method registers a handler for an arbitrary HTTP method.
func (r *Router) Method(method, pattern string, h http.HandlerFunc) *Route {
	r.mux.Method(method, pattern, h)
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
	r.mux.Handle(prefix+"*", http.StripPrefix(prefix, http.FileServerFS(fsys)))
}

// NotFound sets the handler used when no route matches.
func (r *Router) NotFound(h http.HandlerFunc) { r.mux.NotFound(h) }

// MethodNotAllowed sets the handler used when a path matches but the method
// does not.
func (r *Router) MethodNotAllowed(h http.HandlerFunc) { r.mux.MethodNotAllowed(h) }

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
