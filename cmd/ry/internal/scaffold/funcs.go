package scaffold

import (
	"strings"
	"text/template"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
)

// FuncMap is the function set available to every template.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"pascal":   naming.Pascal,
		"camel":    naming.Camel,
		"snake":    naming.Snake,
		"kebab":    naming.Kebab,
		"plural":   naming.Plural,
		"singular": naming.Singular,
		"table":    naming.Table,
		"package":  naming.Package,
		"lower":    strings.ToLower,
		"upper":    strings.ToUpper,
		"quote":    quote,
	}
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\`, `"`, `\"`).Replace(s) + `"`
}
