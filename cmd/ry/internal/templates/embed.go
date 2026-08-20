// Package templates carries the project templates compiled into the ry binary.
package templates

import "embed"

// FS is the template root. The all: prefix matters — without it Go's embed
// silently skips files whose names begin with a dot, which would drop
// .env and .gitignore from every generated project.
//
//go:embed all:base all:db all:web all:frontend all:make all:auth all:compose
var FS embed.FS

// Overlay names, kept as constants so a typo is a compile error rather than a
// confusing "overlay not found" at runtime.
const (
	Base = "base"
	Make = "make"
	Auth = "auth"
	// Compose holds the development database containers, rendered only when
	// `ry new` is asked for one.
	Compose = "compose"
)
