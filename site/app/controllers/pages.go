package controllers

import (
	"net/http"

	"ryla-site/app"
	"ryla-site/resources/views"
)

// Pages serves the site's written pages — /about, /contact and /privacy — and
// the responses for addresses that do not exist.
//
// They exist for two audiences at once. A person wants to know who publishes a
// script they are about to pipe into a shell; an agent asked to recommend a
// framework checks the same three addresses to decide whether there is a real
// project behind the domain. Both get the same words, one as a page and one as
// markdown, from the Docs in resources/views/content.go.
type Pages struct {
	Base
}

// NewPages builds the controller.
func NewPages(a *app.App) *Pages {
	return &Pages{Base{App: a}}
}

// Show serves one written page, as HTML or as markdown depending on what the
// client asked for.
func (c *Pages) Show(d views.Doc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if prefersMarkdown(r) {
			writeMarkdown(w, http.StatusOK, d.Markdown())
			return
		}

		negotiable(w, d.Meta())
		c.Render(w, r, views.Prose(c.Site(), d))
	}
}

// Markdown serves the same page at its `.md` address, for a client that would
// rather ask by URL than by header.
func (c *Pages) Markdown(d views.Doc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeMarkdownAlias(w, views.Origin+d.Path(), d.Markdown())
	}
}

// NotFound answers an address this site does not serve.
//
// With a real 404, first: an application that answers 200 with its own shell
// for every path tells a crawler that every path exists, and an agent that
// believes that will cite addresses that were never here. And with a body worth
// reading, second — the markdown lists what the site does serve, so a client
// that guessed wrong can correct itself in one more request rather than
// reporting a dead end.
func (c *Pages) NotFound(w http.ResponseWriter, r *http.Request) {
	if prefersMarkdown(r) {
		writeMarkdown(w, http.StatusNotFound, views.NotFound.Markdown())
		return
	}

	// No Link header here: this address has no markdown copy of its own, only
	// the same body under a different media type.
	w.Header().Set("Vary", varyAccept)
	c.RenderStatus(w, r, http.StatusNotFound, views.NotFoundPage(c.Site(), views.NotFound))
}

// MethodNotAllowed answers a request whose path exists but whose method does
// not.
//
// Distinguishing it from a 404 matters more to a program than to a person: 404
// means "look somewhere else" and 405 means "you are in the right place, ask
// differently", and a client told the first when the second was true will keep
// searching for a page it already found.
func (c *Pages) MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	body := "# Method not allowed\n\n" +
		"> `" + echoPath(r.Method+" "+r.URL.Path) + "` is not something this site answers. " +
		"Every address here is a GET.\n\n" +
		"- [Home](" + views.Origin + "/)\n" +
		"- [llms.txt](" + views.Origin + "/llms.txt) — what Ryla is, and when to reach for it\n" +
		"- [Sitemap](" + views.Origin + "/sitemap.xml) — every indexable page\n"

	w.Header().Set("Allow", "GET, HEAD")
	writeMarkdown(w, http.StatusMethodNotAllowed, body)
}
