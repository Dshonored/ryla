// Package sqlite registers the SQLite dialect with the db package.
//
// It uses glebarez/sqlite, which wraps the pure-Go modernc.org/sqlite rather
// than mattn/go-sqlite3. That choice is load-bearing: the CGO driver would
// break cross-compilation and the single static binary that is the whole point
// of building this on Go.
//
// Import it for side effects from the application's main package:
//
//	import _ "github.com/Dshonored/ryla/db/sqlite"
package sqlite

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Dshonored/ryla/db"
)

// Driver is the name to use in configuration.
const Driver = "sqlite"

func init() {
	db.Register(Driver, func(dsn string) gorm.Dialector {
		return sqlite.Open(dsn)
	})
}
