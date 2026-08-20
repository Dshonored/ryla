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

// structuredData returns the JSON-LD block for the landing page.
//
// SoftwareSourceCode is the accurate type for a library. SoftwareApplication
// would claim this is something a visitor runs, and Product that it is
// something they buy; a search engine treats a wrong type as a wrong answer.
func structuredData(s Site) string {
	doc := map[string]any{
		"@context":       "https://schema.org",
		"@type":          "SoftwareSourceCode",
		"name":           "Ryla",
		"description":    Description,
		"url":            Origin,
		"codeRepository": s.URL(),
		"license":        "https://opensource.org/licenses/MIT",
		"programmingLanguage": map[string]any{
			"@type": "ComputerLanguage",
			"name":  "Go",
		},
		"runtimePlatform": "Go",
		"author": map[string]any{
			"@type": "Person",
			"name":  "Dshonored",
		},
	}

	// A development build has no released version to advertise. Omitting the
	// field is honest; publishing "dev" as a softwareVersion is not.
	if s.Version != "" && s.Version != "dev" {
		doc["softwareVersion"] = s.Version
	}

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
