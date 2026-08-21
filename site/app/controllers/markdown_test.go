package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNegotiationPicksTheRepresentationTheClientAskedFor is the unit half of
// the acceptmarkdown.com behaviour. The feature tests check that the right body
// comes back from the real routes; this checks the decision itself, including
// the cases a live server would never hand you by accident.
func TestNegotiationPicksTheRepresentationTheClientAskedFor(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		want   bool
	}{
		{"no header at all", "", false},
		{"the convention, exactly", "text/markdown", true},
		{"with a charset parameter", "text/markdown; charset=utf-8", true},
		{"the pre-registration spelling", "text/x-markdown", true},
		{"markdown first, HTML tolerated", "text/markdown, text/html;q=0.9", true},
		{"markdown behind a wildcard it outranks", "text/markdown, */*;q=0.1", true},
		{"case is not significant", "TEXT/MARKDOWN", true},

		// The two that matter most, because they are what actually arrives.
		{"curl and most HTTP clients", "*/*", false},
		{"a browser", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8", false},

		// Quality outranks specificity, as RFC 7231 says it does: a client
		// that downgrades text/html below its wildcard has said it would
		// rather have something else, and markdown is something else.
		{"HTML deliberately downgraded below the wildcard", "text/html;q=0.9, */*", true},
		{"a tie is broken towards the page", "text/html, text/markdown", false},
		{"markdown refused outright", "text/markdown;q=0, text/html", false},
		{"a type wildcard is not a request for markdown", "text/*", false},
		{"something else entirely", "application/json", false},
		{"a malformed q is read as no preference", "text/markdown;q=banana", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.accept != "" {
				r.Header.Set("Accept", tc.accept)
			}

			if got := prefersMarkdown(r); got != tc.want {
				t.Errorf("prefersMarkdown(%q) = %v, want %v", tc.accept, got, tc.want)
			}
		})
	}
}

// TestMarkdownResponsesCarryTheHeadersThatMakeCachesBehave guards the header
// whose absence is invisible until it is expensive: without Vary: Accept a CDN
// serves whichever representation it cached first to everyone who asks for that
// address, and which audience gets the wrong one is decided by who arrived
// first.
func TestMarkdownResponsesCarryTheHeadersThatMakeCachesBehave(t *testing.T) {
	w := httptest.NewRecorder()
	writeMarkdown(w, http.StatusOK, "# hello\n")

	if got := w.Header().Get("Content-Type"); got != MarkdownType {
		t.Errorf("Content-Type = %q, want %q", got, MarkdownType)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept") {
		t.Errorf("Vary = %q, want it to name Accept", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestThePathIsNotEchoedBackVerbatim covers the one place anything from a
// request reaches a response body on this site. The 405 quotes the address that
// was asked for, and an address is whatever the caller typed.
func TestThePathIsNotEchoedBackVerbatim(t *testing.T) {
	got := echoPath("GET /<script>alert(1)</script>`x`")

	for _, unwanted := range []string{"<", ">", "`"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("echoPath kept %q: %s", unwanted, got)
		}
	}

	long := echoPath(strings.Repeat("a", 500))
	if len([]rune(long)) > 81 {
		t.Errorf("echoPath returned %d runes, want it truncated", len([]rune(long)))
	}
}
