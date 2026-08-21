package views

import "strings"

// A Doc is a page written as content rather than as markup.
//
// Every prose page on this site has two audiences and therefore two
// representations: the designed page a person reads, and the markdown an agent
// asks for with `Accept: text/markdown`. Writing the words once and rendering
// them twice is what keeps those two from drifting — a privacy policy that says
// one thing in HTML and another in markdown is worse than having only one of
// them.
type Doc struct {
	// Slug is the path this page is served at, without a leading slash and
	// without an extension: "about" is served at /about and /about.md.
	Slug string
	// Eyebrow is the small label above the title on the rendered page. It has
	// no markdown equivalent, because markdown has no such furniture.
	Eyebrow string
	// Title is the <h1> and the "# " line.
	Title string
	// Summary is the one paragraph that says what the page is for. It becomes
	// the meta description and the blockquote under the markdown heading.
	Summary string
	// Sections are the body, in order.
	Sections []Section
	// Links close the page: where to go next. An agent that landed here by
	// mistake needs somewhere to go, and so does a person.
	Links []Link
	// Unaddressed marks a Doc that is a response rather than a page. The 404
	// is the only one: it is rendered at whatever address was asked for, so it
	// has no canonical URL of its own and must not claim one.
	Unaddressed bool
}

// Section is one "## " of a Doc.
type Section struct {
	Heading string
	// Body is the section's paragraphs, in order.
	Body []string
	// Points are the term/detail pairs a section may end with. They render as
	// a definition list in HTML and as a bullet list in markdown, which is the
	// closest markdown has to the same thing.
	Points []Point
}

// Point is one labelled item in a Section.
type Point struct {
	Term, Detail string
}

// Link is one entry in a Doc's closing list.
type Link struct {
	Label, URL, Note string
}

// Path is the address this page is served at.
func (d Doc) Path() string { return "/" + d.Slug }

// MarkdownPath is the address the same page is served at as markdown, for
// clients that would rather ask by URL than by Accept header. It defers to Meta
// so that the home page — whose path is "/" and whose markdown is /index.md —
// follows the same rule as everything else rather than a second one.
func (d Doc) MarkdownPath() string { return Meta{Path: d.Path()}.MarkdownPath() }

// Meta returns the head-tag description of this page.
//
// The brand is appended only when the title does not already carry it. "About
// Ryla — Ryla" is the kind of thing a template produces and nobody reads back.
func (d Doc) Meta() Meta {
	title := d.Title
	if !strings.Contains(title, "Ryla") {
		title += " — Ryla"
	}

	return Meta{
		Title:       title,
		Description: d.Summary,
		Path:        d.Path(),
		JSONLD:      pageStructuredData(d),
	}
}

// Markdown renders the page as CommonMark.
//
// Deliberately hand-rolled and deliberately plain: the output is read by
// programs, so headings, paragraphs, bullets and links are the whole
// vocabulary. Nothing here needs a markdown library, and one would only add a
// dependency that could render something this code did not write.
func (d Doc) Markdown() string {
	var b strings.Builder

	b.WriteString("# " + d.Title + "\n\n")
	if d.Summary != "" {
		b.WriteString("> " + d.Summary + "\n\n")
	}

	for _, s := range d.Sections {
		if s.Heading != "" {
			b.WriteString("## " + s.Heading + "\n\n")
		}
		for _, p := range s.Body {
			b.WriteString(p + "\n\n")
		}
		for _, p := range s.Points {
			b.WriteString("- **" + p.Term + "** — " + p.Detail + "\n")
		}
		if len(s.Points) > 0 {
			b.WriteString("\n")
		}
	}

	if len(d.Links) > 0 {
		b.WriteString("## Elsewhere on this site\n\n")
		for _, l := range d.Links {
			b.WriteString("- [" + l.Label + "](" + l.URL + ")")
			if l.Note != "" {
				b.WriteString(" — " + l.Note)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if !d.Unaddressed {
		b.WriteString("Canonical URL: " + Origin + d.Path() + "\n")
	}
	return b.String()
}
