package naming

import "testing"

func TestCases(t *testing.T) {
	tests := []struct {
		in                          string
		pascal, camel, snake, kebab string
	}{
		{"user", "User", "user", "user", "user"},
		{"blog_post", "BlogPost", "blogPost", "blog_post", "blog-post"},
		{"BlogPost", "BlogPost", "blogPost", "blog_post", "blog-post"},
		{"blog-post", "BlogPost", "blogPost", "blog_post", "blog-post"},
		{"userID", "UserID", "userID", "user_id", "user-id"},
		{"HTTPServer", "HTTPServer", "httpServer", "http_server", "http-server"},
		{"api_key", "APIKey", "apiKey", "api_key", "api-key"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := Pascal(tc.in); got != tc.pascal {
				t.Errorf("Pascal(%q) = %q, want %q", tc.in, got, tc.pascal)
			}
			if got := Camel(tc.in); got != tc.camel {
				t.Errorf("Camel(%q) = %q, want %q", tc.in, got, tc.camel)
			}
			if got := Snake(tc.in); got != tc.snake {
				t.Errorf("Snake(%q) = %q, want %q", tc.in, got, tc.snake)
			}
			if got := Kebab(tc.in); got != tc.kebab {
				t.Errorf("Kebab(%q) = %q, want %q", tc.in, got, tc.kebab)
			}
		})
	}
}

func TestPluralAndTable(t *testing.T) {
	tests := []struct{ in, plural, table string }{
		{"user", "users", "users"},
		{"category", "categories", "categories"},
		{"box", "boxes", "boxes"},
		{"dish", "dishes", "dishes"},
		{"life", "lives", "lives"},
		{"day", "days", "days"},
		{"status", "statuses", "statuses"},
		{"BlogPost", "BlogPosts", "blog_posts"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := Plural(tc.in); got != tc.plural {
				t.Errorf("Plural(%q) = %q, want %q", tc.in, got, tc.plural)
			}
			if got := Table(tc.in); got != tc.table {
				t.Errorf("Table(%q) = %q, want %q", tc.in, got, tc.table)
			}
		})
	}
}

func TestSingularRoundTrips(t *testing.T) {
	for _, w := range []string{"user", "category", "box", "dish", "day"} {
		if got := Singular(Plural(w)); got != w {
			t.Errorf("Singular(Plural(%q)) = %q, want %q", w, got, w)
		}
	}
}

// TestPluralizeIsIdempotent covers the case that produced "postses":
// `ry make:controller Posts` pluralises a name that is already plural.
func TestPluralizeIsIdempotent(t *testing.T) {
	tests := []struct{ singular, plural string }{
		{"Post", "Posts"},
		{"Category", "Categories"},
		{"Status", "Statuses"},
		{"Class", "Classes"},
		{"Box", "Boxes"},
		{"User", "Users"},
	}

	for _, tc := range tests {
		t.Run(tc.singular, func(t *testing.T) {
			if got := Pluralize(tc.singular); got != tc.plural {
				t.Errorf("Pluralize(%q) = %q, want %q", tc.singular, got, tc.plural)
			}
			if got := Pluralize(tc.plural); got != tc.plural {
				t.Errorf("Pluralize(%q) = %q, want %q (already plural)", tc.plural, got, tc.plural)
			}
		})
	}
}

func TestTableAcceptsSingularOrPlural(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Post", "posts"},
		{"Posts", "posts"},
		{"BlogPost", "blog_posts"},
		{"BlogPosts", "blog_posts"},
		{"Status", "statuses"},
		{"Statuses", "statuses"},
		{"Category", "categories"},
		{"Categories", "categories"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := Table(tc.in); got != tc.want {
				t.Errorf("Table(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
