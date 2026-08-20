// Package mysql registers the MySQL dialect with the db package. It also
// serves MariaDB, which speaks the same protocol.
//
// It uses gorm.io/driver/mysql over go-sql-driver/mysql, which is pure Go, so
// a MySQL build cross-compiles to a single static binary like every other
// driver here.
//
// Import it for side effects from the application's main package:
//
//	import _ "github.com/Dshonored/ryla/db/mysql"
package mysql

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/Dshonored/ryla/db"
)

// Driver is the name to use in configuration.
const Driver = "mysql"

// DefaultStringSize is the width MySQL gives a string column that carries no
// size tag.
//
// Left at the driver's own default, such a column becomes longtext, which
// InnoDB will not index without a prefix length — so the first time someone
// adds an index to a model field they forgot to size, the migration fails on
// MySQL and nowhere else. 255 utf8mb4 characters is 1020 bytes, comfortably
// inside InnoDB's 3072-byte key limit, which makes the column indexable by
// default and matches the size the shipped migrations already spell out.
const DefaultStringSize = 255

func init() {
	db.Register(Driver, func(dsn string) gorm.Dialector {
		return mysql.New(mysql.Config{
			DSN:               dsn,
			DefaultStringSize: DefaultStringSize,
		})
	})
}
