package controllers

import (
	"net/http"
	"strconv"
	"strings"

	"ryla-site/resources/views"
)

// Serving the same page as HTML and as markdown, following the convention
// documented at acceptmarkdown.com: a client that sends `Accept: text/markdown`
// gets markdown, a client that does not gets the page, and the same content is
// also reachable at the page's address with `.md` appended for clients that
// find a URL easier to hold than a header.
//
// Two headers make the difference between that working and appearing to work.
// Vary: Accept tells every cache between here and the client that this address
// has more than one representation — without it a CDN happily hands the HTML it
// cached for a browser to an agent that asked for markdown, and which one an
// address serves comes down to who asked first. And the Link header advertises
// the markdown address to a client that never negotiates at all.
const (
	// MarkdownType is the media type registered for markdown by RFC 7763.
	MarkdownType = "text/markdown; charset=utf-8"

	// HTMLType is what a browser gets from the same addresses.
	HTMLType = "text/html; charset=utf-8"

	// varyAccept names every request header the response depends on.
	// Accept-Encoding is in here because a compressing proxy in front of this
	// application would otherwise be the thing that breaks it.
	varyAccept = "Accept, Accept-Encoding"
)

// negotiable marks a response as one whose body depends on the Accept header,
// and points at the markdown representation of the page.
//
// Called before anything is written: these are headers, and a header set after
// the body has started is a header nobody receives.
func negotiable(w http.ResponseWriter, m views.Meta) {
	w.Header().Set("Vary", varyAccept)
	w.Header().Add("Link", "<"+m.MarkdownURL()+">; rel=\"alternate\"; type=\"text/markdown\"")
}

// writeMarkdown sends the markdown representation of a negotiated page.
//
// nosniff because markdown is a format a browser has no renderer for, and a
// browser with no renderer for a format is a browser that guesses. Saying the
// type is the type settles it.
func writeMarkdown(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", MarkdownType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Vary", varyAccept)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// echoPath renders a requested path for quoting back in a response body.
//
// The only place anything from a request reaches a body on this site, so it is
// the only place that needs this. Control characters, angle brackets and
// backticks are removed rather than escaped: whoever asked for a path
// containing them does not need it read back to them accurately, and a
// markdown body that could smuggle a tag into whatever renders it next is not
// worth the fidelity.
func echoPath(p string) string {
	const limit = 80

	var b strings.Builder
	for i, r := range p {
		if i >= limit {
			b.WriteString("…")
			break
		}
		switch {
		case r < 0x20, r == 0x7f, r == '`', r == '<', r == '>':
			b.WriteRune('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeMarkdownAlias sends the markdown reached through a `.md` address rather
// than through negotiation.
//
// It is the same bytes with two extra claims: this is a copy, and the page it
// is a copy of is the canonical one. Without them the two addresses compete for
// the same search result, and the one that wins is the one a person cannot
// read.
func writeMarkdownAlias(w http.ResponseWriter, canonical, body string) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Add("Link", "<"+canonical+">; rel=\"canonical\"")
	writeMarkdown(w, http.StatusOK, body)
}

// prefersMarkdown reports whether the client asked for markdown rather than the
// page.
//
// RFC 7231 negotiation, decided the way a server is supposed to decide it: the
// most specific matching media range wins, and quality settles the rest. The
// tie is broken towards HTML on purpose. `Accept: */*` — which is what curl
// sends, and what a great many HTTP libraries send by default — matches
// markdown and HTML equally well, and answering a browser-shaped request with
// markdown because it was not picky enough would be a worse failure than
// answering an incurious agent with a page.
func prefersMarkdown(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}

	mdQ, mdSpec := acceptable(accept, "text", "markdown")
	// The pre-registration spelling, still sent by some clients. It never wins
	// on its own merits, only as another way of saying the same thing.
	if q, spec := acceptable(accept, "text", "x-markdown"); q > mdQ || (q == mdQ && spec > mdSpec) {
		mdQ, mdSpec = q, spec
	}
	if mdQ == 0 {
		return false
	}

	htmlQ, htmlSpec := acceptable(accept, "text", "html")
	if mdQ != htmlQ {
		return mdQ > htmlQ
	}
	return mdSpec > htmlSpec
}

// acceptable returns the quality the Accept header gives one media type, and
// how specifically it was named: 2 for an exact match, 1 for a type wildcard,
// 0 for */* or no match at all.
//
// Quality decides first and specificity only breaks a tie, which is the order
// RFC 7231 gives them. That is what makes "*/*" mean "either is fine" — both
// candidates match it equally, so the tie stands and the caller's preference
// for HTML wins — while "text/html;q=0.8, */*" still means what it says, which
// is that the client would rather have something other than the page.
func acceptable(accept, typ, sub string) (quality float64, specificity int) {
	for _, entry := range strings.Split(accept, ",") {
		rangeSpec, params, _ := strings.Cut(strings.TrimSpace(entry), ";")
		rangeType, rangeSub, ok := strings.Cut(strings.TrimSpace(rangeSpec), "/")
		if !ok {
			continue
		}
		rangeType = strings.ToLower(strings.TrimSpace(rangeType))
		rangeSub = strings.ToLower(strings.TrimSpace(rangeSub))

		var spec int
		switch {
		case rangeType == typ && rangeSub == sub:
			spec = 2
		case rangeType == typ && rangeSub == "*":
			spec = 1
		case rangeType == "*" && rangeSub == "*":
			spec = 0
		default:
			continue
		}

		q := quality1(params)
		if spec > specificity || (spec == specificity && q > quality) {
			quality, specificity = q, spec
		}
	}
	return quality, specificity
}

// quality1 reads the q parameter of one media range, defaulting to 1.
func quality1(params string) float64 {
	for _, p := range strings.Split(params, ";") {
		name, value, ok := strings.Cut(p, "=")
		if !ok || strings.ToLower(strings.TrimSpace(name)) != "q" {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || q < 0 {
			// A malformed q is not a reason to drop the whole range; RFC 7231
			// says a value out of range is invalid, and the safe reading of an
			// invalid preference is the default one.
			return 1
		}
		if q > 1 {
			return 1
		}
		return q
	}
	return 1
}
