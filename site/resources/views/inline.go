package views

import (
	"html"
	"strings"
)

// inlineHTML renders one line of a Doc as HTML.
//
// The prose pages are written once and rendered twice — as markdown for an
// agent and as a page for a person — so the source has to be markdown. This is
// the smallest amount of markdown that keeps the HTML honest: `code` becomes a
// <code> element, and a bare URL becomes a link, because a URL a person cannot
// click is a URL they have to retype.
//
// Nothing else is interpreted, and this is not a markdown parser. Everything is
// escaped first and the output is assembled from escaped fragments, so a page
// that one day quotes a tag cannot inject one.
func inlineHTML(s string) string {
	var b strings.Builder

	// Split on backticks: even-numbered fragments are prose, odd-numbered ones
	// are code. An unterminated backtick leaves a final odd fragment, which is
	// rendered as prose rather than swallowed.
	parts := strings.Split(s, "`")
	for i, part := range parts {
		code := i%2 == 1 && i < len(parts)-1
		if code {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
			continue
		}
		b.WriteString(linkify(html.EscapeString(part)))
	}
	return b.String()
}

// linkify turns bare http(s) URLs in already-escaped text into anchors.
func linkify(escaped string) string {
	var b strings.Builder

	rest := escaped
	for {
		i := strings.Index(rest, "https://")
		if i < 0 {
			i = strings.Index(rest, "http://")
		}
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}

		b.WriteString(rest[:i])

		// The URL runs to the first character that cannot be in one.
		end := len(rest)
		for j := i; j < len(rest); j++ {
			if c := rest[j]; c == ' ' || c == '\t' || c == '"' || c == '<' || c == ')' {
				end = j
				break
			}
		}

		url := rest[i:end]
		// A sentence ending in a URL puts its full stop inside the match.
		// Trimming it is what makes the link work and the sentence read.
		trimmed := strings.TrimRight(url, ".,;:")
		tail := url[len(trimmed):]

		b.WriteString(`<a href="` + trimmed + `" rel="noopener">` + trimmed + `</a>` + tail)
		rest = rest[end:]
	}
}
