// Package postgres registers the PostgreSQL dialect with the db package.
//
// It uses gorm.io/driver/postgres, which talks to the server through pgx
// rather than libpq. That matters for the same reason the SQLite choice does:
// pgx is pure Go, so a Postgres build still cross-compiles to a single static
// binary with no C toolchain in sight.
//
// Import it for side effects from the application's main package:
//
//	import _ "github.com/Dshonored/ryla/db/postgres"
package postgres

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Dshonored/ryla/db"
)

// Driver is the name to use in configuration.
const Driver = "postgres"

func init() {
	db.Register(Driver, func(dsn string) gorm.Dialector {
		return postgres.Open(dsn)
	})
}
