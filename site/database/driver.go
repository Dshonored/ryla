// Package database selects the SQL driver this project was generated for.
//
// Importing it registers the dialect with the framework's db package. Only the
// chosen driver is linked into the binary, so a SQLite build carries no
// Postgres or MySQL code.
package database

import (
	// Registers the "sqlite" dialect. glebarez/sqlite wraps the pure-Go
	// modernc.org/sqlite rather than the CGO one, which is what lets this
	// application cross-compile to a single static binary.
	_ "github.com/Dshonored/ryla/db/sqlite"
)

// Driver is the dialect name used in configuration.
const Driver = "sqlite"

// DefaultDSN points at a file under storage/.
//
// The pragmas matter: SQLite does not enforce foreign keys unless asked, and
// without a busy timeout any concurrent write fails immediately instead of
// waiting for the lock to clear.
const DefaultDSN = "storage/ryla-site.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
