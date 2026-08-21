// Feature tests for the parts of this site read by something other than a
// person: the markdown representations, the 404, the pages an agent checks
// before it recommends anything, and the structured data.
//
// They matter more than they look. None of what follows is visible on the page,
// so every one of these can break without anything appearing wrong — the site
// goes on rendering perfectly while quietly telling machines something false.
package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	rytest "github.com/Dshonored/ryla/testing"

	"ryla-site/resources/views"
)

// get issues a GET carrying an Accept header, which is the whole subject of
// half the tests below.
func get(t *testing.T, path, accept string) *rytest.Response {
	t.Helper()

	c := client(t)
	req := c.Request(http.MethodGet, path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return c.Do(req)
}

// The pages that are served both ways. Written out rather than derived from
// views.Docs so that a page removed from the site fails this test instead of
// silently leaving it with less to check.
var negotiated = []struct{ path, markdownPath string }{
	{"/", "/index.md"},
	{"/about", "/about.md"},
	{"/contact", "/contact.md"},
	{"/privacy", "/privacy.md"},
}

// TestEveryPageServesMarkdownWhenAsked covers the acceptmarkdown.com
// convention: ask an address for text/markdown and get markdown from it, with
// Vary: Accept so that no cache in between can hand the answer to the wrong
// audience.
func TestEveryPageServesMarkdownWhenAsked(t *testing.T) {
	for _, page := range negotiated {
		t.Run(page.path, func(t *testing.T) {
			r := get(t, page.path, "text/markdown").
				AssertOK().
				AssertHeader("Content-Type", "text/markdown; charset=utf-8")

			if vary := r.Header().Get("Vary"); !strings.Contains(vary, "Accept") {
				t.Errorf("Vary = %q, want it to name Accept", vary)
			}
			if body := r.Body(); !strings.HasPrefix(body, "# ") {
				t.Errorf("the body does not begin with a markdown heading; got %.40q", body)
			}
		})
	}
}

// TestThePageIsStillThePageForEveryoneElse is the other half of the same
// contract, and the one that would be embarrassing to break: a browser, and a
// client that says only "*/*", must still get the page.
func TestThePageIsStillThePageForEveryoneElse(t *testing.T) {
	accepts := []string{
		"",
		"*/*",
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}

	for _, accept := range accepts {
		for _, page := range negotiated {
			r := get(t, page.path, accept).
				AssertOK().
				AssertHeader("Content-Type", "text/html; charset=utf-8")

			if vary := r.Header().Get("Vary"); !strings.Contains(vary, "Accept") {
				t.Errorf("%s with Accept %q: Vary = %q, want it to name Accept", page.path, accept, vary)
			}

			// RFC 8288, for a client that never negotiates: the markdown copy
			// is advertised rather than left to be guessed at.
			link := r.Header().Get("Link")
			if !strings.Contains(link, views.Origin+page.markdownPath) || !strings.Contains(link, `type="text/markdown"`) {
				t.Errorf("%s: Link = %q, want it to advertise %s as markdown", page.path, link, page.markdownPath)
			}
		}
	}
}

// TestTheMarkdownAddressesServeTheSameWords checks the second way of asking —
// the address with .md appended — and the two headers that stop it competing
// with the page for the same search result.
func TestTheMarkdownAddressesServeTheSameWords(t *testing.T) {
	for _, page := range negotiated {
		t.Run(page.markdownPath, func(t *testing.T) {
			byURL := client(t).Get(page.markdownPath).
				AssertOK().
				AssertHeader("Content-Type", "text/markdown; charset=utf-8").
				AssertHeader("X-Robots-Tag", "noindex, nofollow")

			canonical := views.Origin + page.path
			if link := byURL.Header().Get("Link"); !strings.Contains(link, canonical) || !strings.Contains(link, `rel="canonical"`) {
				t.Errorf("Link = %q, want it to name %s as canonical", link, canonical)
			}

			byHeader := get(t, page.path, "text/markdown").AssertOK()
			if byURL.Body() != byHeader.Body() {
				t.Errorf("%s and %s with Accept: text/markdown returned different bodies", page.markdownPath, page.path)
			}
		})
	}
}

// TestAnAddressThatDoesNotExistSaysSoAndSaysWhereToGo is the one every agent
// audit checks first, and the one an application framework gets wrong by
// default: answering 200 with a shell for every path tells a crawler that every
// path exists.
//
// The status is half of it. The other half is that the body is worth reading —
// a client that guessed wrong should be able to correct itself from the answer
// rather than reporting a dead end.
func TestAnAddressThatDoesNotExistSaysSoAndSaysWhereToGo(t *testing.T) {
	const missing = "/some-path-that-does-not-exist"

	md := get(t, missing, "text/markdown").
		AssertStatus(http.StatusNotFound).
		AssertHeader("Content-Type", "text/markdown; charset=utf-8").
		AssertContains(
			"# Page not found",
			views.Origin+"/llms.txt",
			views.Origin+"/sitemap.xml",
			"("+views.Origin+"/about)",
		)

	if vary := md.Header().Get("Vary"); !strings.Contains(vary, "Accept") {
		t.Errorf("Vary = %q, want it to name Accept", vary)
	}

	// A 404 has no address of its own, so it must not claim one.
	md.AssertNotContains("Canonical URL:")

	// The page a person gets carries the same recovery instructions, verbatim,
	// so a client that only ever reads rendered HTML is not worse off.
	get(t, missing, "text/html").
		AssertStatus(http.StatusNotFound).
		AssertContains(
			"Page not found",
			"noindex",
			views.Origin+"/llms.txt",
			views.Origin+"/sitemap.xml",
		)
}

// TestAWrongMethodIsNotAWrongAddress. The distinction is worth more to a
// program than to a person: 404 means look elsewhere, 405 means you are in the
// right place and asked the wrong way.
func TestAWrongMethodIsNotAWrongAddress(t *testing.T) {
	c := client(t)
	r := c.Do(c.Request(http.MethodOptions, "/about", nil)).
		AssertStatus(http.StatusMethodNotAllowed).
		AssertHeader("Allow", "GET, HEAD").
		AssertContains("# Method not allowed", views.Origin+"/llms.txt")

	if strings.Contains(r.Body(), "<") {
		t.Errorf("the 405 body is not plain markdown: %.80q", r.Body())
	}
}

// TestTheTrustAnchorPagesAreRealPages covers what an agent checks before it
// recommends a project to anyone: that About, Contact and Privacy exist, are
// reachable, and actually say something.
//
// The length assertion is deliberately crude. It is not a measure of quality —
// it is the difference between a page and a placeholder, which is the thing
// that rots first when nobody is looking at these three.
func TestTheTrustAnchorPagesAreRealPages(t *testing.T) {
	for _, d := range views.Docs {
		t.Run(d.Slug, func(t *testing.T) {
			body := d.Markdown()
			if len(body) < 500 {
				t.Errorf("/%s has %d characters of content, want at least 500", d.Slug, len(body))
			}

			client(t).Get(d.Path()).
				AssertOK().
				AssertContains(d.Title, d.Summary)
		})
	}

	// The specifics each page exists to state. Vague pages pass a length check
	// and help nobody.
	client(t).Get("/about").AssertOK().AssertContains("MIT", "pre-1.0", views.Maintainer)
	client(t).Get("/contact").AssertOK().AssertContains("/issues", "Security")
	client(t).Get("/privacy").AssertOK().AssertContains("ryla_csrf", "fonts.googleapis.com")
}

// TestTheWrittenPagesAreReachableFromEveryPage. A trust anchor nobody can find
// from the landing page is one a crawler will not find either.
func TestTheWrittenPagesAreReachableFromEveryPage(t *testing.T) {
	for _, from := range []string{"/", "/about", "/privacy"} {
		body := client(t).Get(from).AssertOK().Body()
		for _, to := range []string{`href="/about"`, `href="/contact"`, `href="/privacy"`, `href="/llms.txt"`} {
			if !strings.Contains(body, to) {
				t.Errorf("%s does not link to %s", from, to)
			}
		}
	}

	client(t).Get("/sitemap.xml").
		AssertOK().
		AssertContains(
			"<loc>"+views.Origin+"/</loc>",
			"<loc>"+views.Origin+"/about</loc>",
			"<loc>"+views.Origin+"/contact</loc>",
			"<loc>"+views.Origin+"/privacy</loc>",
		).
		// The markdown copies are the same content at a second address. Listing
		// them is how a site ends up competing with itself for its own name.
		AssertNotContains(".md</loc>")
}

// TestTheSiteSaysWhenAnAgentShouldReachForIt. The audit item this answers is
// not "is there an llms.txt" but "does it say what jobs this is for" — a model
// deciding whether to recommend a framework needs the second one, and generic
// marketing copy does not read as guidance.
func TestTheSiteSaysWhenAnAgentShouldReachForIt(t *testing.T) {
	client(t).Get("/llms.txt").
		AssertOK().
		AssertContains(
			"## When to use Ryla",
			"## When not to use Ryla",
			// Specific jobs, not adjectives.
			"ry new",
			"ry make:auth",
			// The refusals matter as much: a file that fits everything is
			// discounted as marketing, correctly.
			"The project is not in Go",
			"not built",
			views.Origin+"/agents.md",
		)

	client(t).Get("/agents.md").
		AssertOK().
		AssertHeader("Content-Type", "text/markdown; charset=utf-8").
		AssertContains(
			"## When to use Ryla",
			"## When not to use Ryla",
			"## How to call it",
			"ry new myapp",
			"--json",
			"AGENTS.md",
		)

	// robots.txt is the first address a crawler asks for, which makes it the
	// one place a pointer to the rest is certain to be seen.
	client(t).Get("/robots.txt").
		AssertOK().
		AssertContains(views.Origin+"/llms.txt", views.Origin+"/agents.md")
}

// TestTheStructuredDataIdentifiesTheProjectAndItsAuthor. Structured data fails
// silently by construction: a field left out costs nothing visible, and the
// only symptom is a machine describing the project less completely than it
// could have.
func TestTheStructuredDataIdentifiesTheProjectAndItsAuthor(t *testing.T) {
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(firstJSONLD(t, client(t).Get("/").AssertOK().Body())), &doc); err != nil {
		t.Fatalf("the landing page's JSON-LD does not parse: %v", err)
	}
	if doc.Context != "https://schema.org" {
		t.Errorf("@context = %q, want https://schema.org", doc.Context)
	}

	byType := map[string]map[string]any{}
	for _, node := range doc.Graph {
		typ, _ := node["@type"].(string)
		byType[typ] = node
	}

	for _, want := range []string{"SoftwareSourceCode", "SoftwareApplication", "WebSite", "Person"} {
		node, ok := byType[want]
		if !ok {
			t.Fatalf("the graph has no %s node", want)
		}
		// Every node stands on its own, because a consumer may read only the
		// one whose type it was looking for.
		for _, field := range []string{"name", "description", "url"} {
			if v, _ := node[field].(string); v == "" {
				t.Errorf("the %s node has no %s", want, field)
			}
		}
	}

	// The three fields the audit named as missing from the Person.
	person := byType["Person"]
	if got, _ := person["url"].(string); got != views.MaintainerURL {
		t.Errorf("the Person url = %q, want %q", got, views.MaintainerURL)
	}
	if got, _ := person["jobTitle"].(string); got == "" {
		t.Error("the Person has no jobTitle")
	}
	if same, _ := person["sameAs"].([]any); len(same) == 0 {
		t.Error("the Person has no sameAs, so nothing ties this identity to one that already exists")
	}
}

// TestTheWrittenPagesDescribeThemselvesToo. AboutPage and ContactPage are the
// two types a consumer verifying a business looks for by name.
func TestTheWrittenPagesDescribeThemselvesToo(t *testing.T) {
	cases := map[string]string{"/about": "AboutPage", "/contact": "ContactPage", "/privacy": "WebPage"}

	for path, want := range cases {
		var node map[string]any
		if err := json.Unmarshal([]byte(firstJSONLD(t, client(t).Get(path).AssertOK().Body())), &node); err != nil {
			t.Fatalf("%s: the JSON-LD does not parse: %v", path, err)
		}

		if got, _ := node["@type"].(string); got != want {
			t.Errorf("%s: @type = %q, want %q", path, got, want)
		}
		if got, _ := node["url"].(string); got != views.Origin+path {
			t.Errorf("%s: url = %q, want %q", path, got, views.Origin+path)
		}
		if publisher, ok := node["publisher"].(map[string]any); !ok || publisher["name"] != views.Maintainer {
			t.Errorf("%s: the page names no publisher", path)
		}
	}
}

// TestEachPageDeclaresItsOwnAddress guards the mistake that arrives with the
// second page: a layout that hard-codes the canonical URL is wrong everywhere
// at once, breaks nothing visibly, and is reported weeks later by a search
// engine.
func TestEachPageDeclaresItsOwnAddress(t *testing.T) {
	for _, page := range negotiated {
		body := client(t).Get(page.path).AssertOK().Body()

		canonical := views.Origin + page.path
		if page.path == "/" {
			canonical = views.Origin + "/"
		}
		want := []string{
			`<link rel="canonical" href="` + canonical + `">`,
			`<meta property="og:url" content="` + canonical + `">`,
			`<link rel="alternate" type="text/markdown" href="` + views.Origin + page.markdownPath + `"`,
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s does not contain %s", page.path, w)
			}
		}
	}
}

// firstJSONLD returns the contents of the page's first JSON-LD block.
func firstJSONLD(t *testing.T, body string) string {
	t.Helper()

	const open = `<script type="application/ld+json">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("the page carries no JSON-LD")
	}

	rest := body[i+len(open):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("the page's JSON-LD block is not closed")
	}
	return rest[:end]
}
