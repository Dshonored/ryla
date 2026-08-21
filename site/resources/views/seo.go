package views

import "encoding/json"

// Origin is the canonical address of the site. Every absolute URL on the page
// is built from it, so moving the site is one edit rather than a search.
const Origin = "https://ryla.io"

// Description is the one sentence that appears in a search result and in a link
// preview. It is deliberately the same sentence the README opens with: a
// project whose pitch changes depending on where you read it does not have one.
const Description = "A batteries-included web framework for Go. Routing, ORM, migrations, " +
	"auth, queues, mail, cache and a scheduler already wired together, and a CLI that writes " +
	"the boilerplate as real Go files you can read and delete."

// SearchConsoleToken proves to Google that whoever controls this repository
// controls the site.
//
// It is a verification token, not a credential: it grants nothing, and it is
// visible in the page source of every site that uses this method. Keeping it in
// the repository rather than in a file dropped on a server is what makes the
// verification survive a redeploy — the site is rebuilt from this tree, so
// anything not committed here would silently disappear on the next push and
// Google would eventually unverify the property.
const SearchConsoleToken = "sS3cDsfvnwRiovSNdhR2Ne3A5yOHfL8_i5TDj5dHpYI"

// The @id values that let the nodes below refer to each other instead of
// repeating themselves. A consumer that follows them gets one graph describing
// one project; a consumer that does not still gets four complete nodes.
const (
	idSource  = Origin + "/#ryla"
	idCLI     = Origin + "/#ry"
	idSite    = Origin + "/#website"
	idAuthor  = MaintainerURL + "#me"
	licenseMI = "https://opensource.org/licenses/MIT"
)

// structuredData returns the JSON-LD block for the landing page.
//
// One graph rather than one node, because "Ryla" is honestly several things at
// once and a consumer looking for any of them should find it: the source of a
// library, the command-line application that source installs, the website you
// are reading, and the person who publishes all three. Splitting them is what
// lets each node be accurate — SoftwareSourceCode has a repository and a
// licence, SoftwareApplication has an operating system and a price, and neither
// has to pretend to be the other.
//
// SoftwareSourceCode leads the graph because that is what ryla.io is primarily
// about. SoftwareApplication would be the wrong headline for a library, and
// Product would claim there is something to buy.
func structuredData(s Site) string {
	author := map[string]any{
		"@type":       "Person",
		"@id":         idAuthor,
		"name":        Maintainer,
		"url":         MaintainerURL,
		"jobTitle":    "Software developer",
		"description": "Author and maintainer of Ryla, a batteries-included web framework for Go.",
		"sameAs":      []string{MaintainerURL, Repository},
	}

	source := map[string]any{
		"@type":          "SoftwareSourceCode",
		"@id":            idSource,
		"name":           "Ryla",
		"alternateName":  "Ryla for Go",
		"description":    Description,
		"url":            Origin + "/",
		"codeRepository": s.URL(),
		"license":        licenseMI,
		"sameAs":         []string{s.URL(), "https://pkg.go.dev/github.com/Dshonored/ryla"},
		"programmingLanguage": map[string]any{
			"@type": "ComputerLanguage",
			"name":  "Go",
		},
		"runtimePlatform": "Go",
		"keywords":        "go, golang, web framework, laravel, orm, migrations, cli, scaffolding",
		"author":          author,
		"maintainer":      map[string]any{"@id": idAuthor},
		"targetProduct":   map[string]any{"@id": idCLI},
	}

	cli := map[string]any{
		"@type":               "SoftwareApplication",
		"@id":                 idCLI,
		"name":                "ry",
		"alternateName":       "Ryla CLI",
		"description":         "The Ryla command-line tool. Scaffolds a Go project, runs it with live reload, applies migrations, generates controllers, models, jobs and tests, and compiles the whole application into one static binary.",
		"url":                 Origin + "/",
		"downloadUrl":         Origin + "/install.sh",
		"installUrl":          Origin + "/install.sh",
		"applicationCategory": "DeveloperApplication",
		"operatingSystem":     "macOS, Linux, Windows",
		"license":             licenseMI,
		"sameAs":              []string{s.URL()},
		"author":              map[string]any{"@id": idAuthor},
		"isBasedOn":           map[string]any{"@id": idSource},
		// Free, and said in the form a consumer can actually read. "Open
		// source" in prose is not a price.
		"offers": map[string]any{
			"@type":         "Offer",
			"price":         "0",
			"priceCurrency": "USD",
			"availability":  "https://schema.org/InStock",
			"url":           Origin + "/",
		},
	}

	site := map[string]any{
		"@type":         "WebSite",
		"@id":           idSite,
		"name":          "Ryla",
		"alternateName": []string{"ryla.io", "Ryla framework"},
		"description":   Description,
		"url":           Origin + "/",
		"inLanguage":    "en",
		"about":         map[string]any{"@id": idSource},
		"publisher":     map[string]any{"@id": idAuthor},
	}

	// A development build has no released version to advertise. Omitting the
	// field is honest; publishing "dev" as a softwareVersion is not.
	if s.Version != "" && s.Version != "dev" {
		source["softwareVersion"] = s.Version
		cli["softwareVersion"] = s.Version
	}

	return jsonLD(map[string]any{
		"@context": "https://schema.org",
		"@graph":   []any{source, cli, site, author},
	})
}

// pageStructuredData returns the JSON-LD block for a prose page.
//
// AboutPage and ContactPage are not decoration: they are the two types a
// consumer checking whether a project is a real thing looks for by name, and
// answering with a bare WebPage makes it do the guessing instead.
func pageStructuredData(d Doc) string {
	pageType := "WebPage"
	switch d.Slug {
	case "about":
		pageType = "AboutPage"
	case "contact":
		pageType = "ContactPage"
	}

	return jsonLD(map[string]any{
		"@context":    "https://schema.org",
		"@type":       pageType,
		"name":        d.Title,
		"headline":    d.Title,
		"description": d.Summary,
		"url":         Origin + d.Path(),
		"inLanguage":  "en",
		"isPartOf":    map[string]any{"@id": idSite},
		"about":       map[string]any{"@id": idSource},
		"publisher": map[string]any{
			"@type":    "Person",
			"@id":      idAuthor,
			"name":     Maintainer,
			"url":      MaintainerURL,
			"jobTitle": "Software developer",
			"sameAs":   []string{MaintainerURL, Repository},
		},
		"license": licenseMI,
	})
}

// jsonLD marshals a document into the script tag that carries it.
func jsonLD(doc map[string]any) string {
	raw, err := json.Marshal(doc)
	if err != nil {
		// Nothing here is user input, so this cannot fail in practice — and a
		// page with no structured data beats one carrying a broken block.
		return ""
	}

	// This block sits inside <script>, where a literal </script> in any value
	// would break out of it. json.Marshal escapes <, > and & by default, so
	// that cannot happen — which is why this is marshalled rather than
	// assembled with fmt.Sprintf.
	return `<script type="application/ld+json">` + string(raw) + `</script>`
}
