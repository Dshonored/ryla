// Package naming converts identifiers between the cases the generators need.
//
// Laravel leans on runtime string helpers for this; here it happens once, at
// generation time, so the code that ships is plain Go with no naming magic left
// in it.
package naming

import (
	"strings"
	"unicode"
)

// Words splits an identifier into lowercase words. It understands snake_case,
// kebab-case, space separated text and camelCase/PascalCase, including runs of
// capitals such as "HTTPServer" -> ["http", "server"].
func Words(s string) []string {
	var words []string
	var cur []rune

	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.' || r == '/':
			flush()
		case unicode.IsUpper(r):
			// Start a new word at a lower->upper boundary, and at the end of a
			// run of capitals followed by a lowercase letter (HTTPServer).
			prevLower := i > 0 && unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || (len(cur) > 0 && nextLower) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// Pascal returns PascalCase, used for Go type and exported function names.
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range Words(s) {
		b.WriteString(capitalize(w))
	}
	return b.String()
}

// Camel returns camelCase, used for unexported identifiers.
func Camel(s string) string {
	words := Words(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(words[0])
	for _, w := range words[1:] {
		b.WriteString(capitalize(w))
	}
	return b.String()
}

// Snake returns snake_case, used for table and column names.
func Snake(s string) string { return strings.Join(Words(s), "_") }

// Kebab returns kebab-case, used for URL segments and file names.
func Kebab(s string) string { return strings.Join(Words(s), "-") }

// Package returns a valid lowercase Go package name.
func Package(s string) string {
	name := strings.Join(Words(s), "")
	if name == "" {
		return "app"
	}
	if unicode.IsDigit(rune(name[0])) {
		return "app" + name
	}
	return name
}

// Plural returns the plural of an English word. It covers the common rules —
// enough for table names and route segments — and nothing more; anything
// irregular should be spelled out explicitly by the developer.
func Plural(s string) string {
	if s == "" {
		return s
	}

	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "s"),
		strings.HasSuffix(lower, "x"),
		strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"),
		strings.HasSuffix(lower, "sh"):
		return s + "es"
	case strings.HasSuffix(lower, "y") && len(s) > 1 && !isVowel(rune(lower[len(lower)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(lower, "fe"):
		return s[:len(s)-2] + "ves"
	case strings.HasSuffix(lower, "f"):
		return s[:len(s)-1] + "ves"
	default:
		return s + "s"
	}
}

// Singular reverses the common Plural rules.
//
// Words ending in -ss, -us or -is are left alone: "class", "status" and
// "analysis" are already singular, and stripping the trailing s would turn
// them into nonsense that then pluralises back to the wrong table name.
func Singular(s string) string {
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "ss"),
		strings.HasSuffix(lower, "us"),
		strings.HasSuffix(lower, "is"):
		return s
	case strings.HasSuffix(lower, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(lower, "ves") && len(s) > 3:
		return s[:len(s)-3] + "f"
	case strings.HasSuffix(lower, "ses"),
		strings.HasSuffix(lower, "xes"),
		strings.HasSuffix(lower, "zes"),
		strings.HasSuffix(lower, "ches"),
		strings.HasSuffix(lower, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(lower, "s"):
		return s[:len(s)-1]
	default:
		return s
	}
}

// Pluralize returns the plural form of a word whether it arrives singular or
// already plural.
//
// Generators need this because naming conventions disagree: a model is named
// Post but a controller is named Posts, and both have to produce the same
// "posts" table and URL segment.
func Pluralize(s string) string { return Plural(Singular(s)) }

// Table returns the snake_case plural table name for a model. It accepts either
// "Post" or "Posts" and answers "posts" for both.
func Table(model string) string {
	words := Words(model)
	if len(words) == 0 {
		return ""
	}
	words[len(words)-1] = Pluralize(words[len(words)-1])
	return strings.Join(words, "_")
}

func capitalize(w string) string {
	if w == "" {
		return w
	}
	// Keep the initialisms Go style guides insist on uppercase.
	switch w {
	case "id", "url", "uri", "api", "http", "https", "html", "json", "sql", "db", "ip", "uuid":
		return strings.ToUpper(w)
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func isVowel(r rune) bool { return strings.ContainsRune("aeiou", r) }
