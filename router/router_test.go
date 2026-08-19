package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func noop(http.ResponseWriter, *http.Request) {}

func TestURLBuilding(t *testing.T) {
	r := New()
	r.Get("/", noop).Name("home")
	r.Get("/users/{id}", noop).Name("users.show")
	r.Get("/users/{id}/posts/{postID}", noop).Name("posts.show")
	r.Get("/files/{id:[0-9]{4}}", noop).Name("files.show")
	r.Get("/users", noop).Name("users.index")

	tests := []struct {
		name   string
		route  string
		params []any
		want   string
	}{
		{"static", "home", nil, "/"},
		{"one param", "users.show", []any{"id", 42}, "/users/42"},
		{"two params", "posts.show", []any{"id", 1, "postID", "abc"}, "/users/1/posts/abc"},
		{"regexp constraint", "files.show", []any{"id", 2026}, "/files/2026"},
		{"extra becomes query", "users.index", []any{"page", 2}, "/users?page=2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.URL(tc.route, tc.params...)
			if err != nil {
				t.Fatalf("URL(%q) returned error: %v", tc.route, err)
			}
			if got != tc.want {
				t.Errorf("URL(%q) = %q, want %q", tc.route, got, tc.want)
			}
		})
	}
}

func TestURLErrors(t *testing.T) {
	r := New()
	r.Get("/users/{id}", noop).Name("users.show")

	tests := []struct {
		name   string
		route  string
		params []any
	}{
		{"unknown route", "nope", nil},
		{"missing param", "users.show", nil},
		{"odd params", "users.show", []any{"id"}},
		{"non-string key", "users.show", []any{1, 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.URL(tc.route, tc.params...); err == nil {
				t.Fatalf("URL(%q, %v) succeeded, want error", tc.route, tc.params)
			}
		})
	}
}

func TestPrefixesAreRecordedInNames(t *testing.T) {
	r := New()
	r.Route("/admin", func(r *Router) {
		r.Route("/settings", func(r *Router) {
			r.Get("/{key}", noop).Name("admin.settings.show")
		})
	})

	got, err := r.URL("admin.settings.show", "key", "theme")
	if err != nil {
		t.Fatalf("URL returned error: %v", err)
	}
	if want := "/admin/settings/theme"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestGroupMiddlewareIsScoped(t *testing.T) {
	r := New()
	r.Get("/open", noop)
	r.Group(func(r *Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("X-Guarded", "1")
				next.ServeHTTP(w, req)
			})
		})
		r.Get("/closed", noop)
	})

	for path, wantHeader := range map[string]string{"/open": "", "/closed": "1"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("X-Guarded"); got != wantHeader {
			t.Errorf("GET %s: X-Guarded = %q, want %q", path, got, wantHeader)
		}
	}
}

func TestDuplicateNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a duplicate route name did not panic")
		}
	}()

	r := New()
	r.Get("/a", noop).Name("dup")
	r.Get("/b", noop).Name("dup")
}

func TestRoutingStillWorks(t *testing.T) {
	r := New()
	r.Get("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(Param(req, "id")))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/7", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "7" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "7")
	}
}
