package release

import "testing"

// TestEscapeModule guards the rule that makes the proxy lookup work at all:
// the module proxy lowercases uppercase letters behind a "!", so
// github.com/Dshonored/ryla is requested as github.com/!dshonored/ryla. Get
// this wrong and every update check silently 404s.
func TestEscapeModule(t *testing.T) {
	tests := []struct{ in, want string }{
		{"github.com/Dshonored/ryla", "github.com/!dshonored/ryla"},
		{"github.com/lowercase/only", "github.com/lowercase/only"},
		{"github.com/AB/CD", "github.com/!a!b/!c!d"},
		{"gorm.io/gorm", "gorm.io/gorm"},
	}

	for _, tc := range tests {
		if got := escapeModule(tc.in); got != tc.want {
			t.Errorf("escapeModule(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name            string
		current, latest string
		want            bool
	}{
		{"newer patch", "v0.1.0", "v0.1.1", true},
		{"newer minor", "v0.1.9", "v0.2.0", true},
		{"same", "v0.1.0", "v0.1.0", false},
		{"older latest", "v0.2.0", "v0.1.0", false},
		{"no latest", "v0.1.0", "", false},

		// A build made from a working tree is not "behind" a release: its code
		// is not on the proxy at all, so the comparison is meaningless and the
		// nudge would be actively wrong.
		{"dev build", "dev", "v9.9.9", false},
		{"dirty build", "v0.1.1-0.20260819175734-42a4734a3bbb+dirty", "v9.9.9", false},

		{"garbage current", "not-a-version", "v0.1.0", false},
		{"garbage latest", "v0.1.0", "not-a-version", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNewer(tc.current, tc.latest); got != tc.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestProxyBaseHonoursGOPROXY(t *testing.T) {
	tests := []struct{ goproxy, want string }{
		{"", "https://proxy.golang.org"},
		{"off", "https://proxy.golang.org"},
		{"direct", "https://proxy.golang.org"},
		{"https://corp.example/mod", "https://corp.example/mod"},
		// GOPROXY is a fallback list; the first usable entry is the one the go
		// command would reach for first.
		{"https://corp.example/mod,direct", "https://corp.example/mod"},
		{"direct|https://corp.example/mod", "https://corp.example/mod"},
	}

	for _, tc := range tests {
		t.Setenv("GOPROXY", tc.goproxy)
		if got := proxyBase(); got != tc.want {
			t.Errorf("proxyBase() with GOPROXY=%q = %q, want %q", tc.goproxy, got, tc.want)
		}
	}
}

func TestDisabledByEnvironment(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("RYLA_NO_UPDATE_CHECK", "")
	if disabled() {
		t.Error("update check should be enabled with neither variable set")
	}

	t.Setenv("RYLA_NO_UPDATE_CHECK", "1")
	if !disabled() {
		t.Error("RYLA_NO_UPDATE_CHECK=1 should disable the check")
	}

	// Nobody wants a version nudge in build logs.
	t.Setenv("RYLA_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "true")
	if !disabled() {
		t.Error("CI should disable the check")
	}
}
