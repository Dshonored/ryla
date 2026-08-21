package views

import "strings"

// Meta is what the layout needs to describe one page to a machine: the title,
// the sentence a search result shows, and the address it is canonically served
// at.
//
// It exists because the site now has more than one page. A layout that hard-
// codes the canonical URL is right exactly until the second page is added, and
// then it is quietly wrong on all of them — the kind of mistake that costs
// nothing to make, breaks nothing visibly, and takes a search engine weeks to
// tell you about.
type Meta struct {
	// Title is the <title> and the og:title.
	Title string
	// Description is the meta description, the og:description and the
	// twitter:description.
	Description string
	// Path is the path this page is served at, beginning with a slash.
	Path string
	// NoIndex keeps a page out of search results. Only the error pages set it:
	// an indexed 404 is a result that wastes everyone's time.
	NoIndex bool
	// JSONLD is the structured-data block for this page, already wrapped in
	// its script tag. It rides along with the rest of the head description
	// because it says the same things — the name, the sentence and the
	// address — and two places to keep those in step is one too many.
	JSONLD string
}

// Canonical is the absolute address of this page.
func (m Meta) Canonical() string {
	if m.Path == "" {
		return Origin + "/"
	}
	return Origin + m.Path
}

// MarkdownPath is where the same page is served as markdown.
//
// The convention is the page's own address with `.md` appended, which leaves
// the home page needing a name of its own — /index.md, the one filename that
// has always meant "this directory".
func (m Meta) MarkdownPath() string {
	if m.Path == "" || m.Path == "/" {
		return "/index.md"
	}
	return strings.TrimSuffix(m.Path, "/") + ".md"
}

// MarkdownURL is the absolute form, for the Link header and the <link> tag that
// advertise the markdown representation to clients that never send an Accept
// header worth negotiating on.
func (m Meta) MarkdownURL() string { return Origin + m.MarkdownPath() }
