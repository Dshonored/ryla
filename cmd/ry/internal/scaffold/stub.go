package scaffold

import (
	"time"

	"github.com/Dshonored/ryla/cmd/ry/internal/naming"
)

// Stub is the data a make: generator renders its template against. Every case
// of the name is precomputed so templates never call helpers for the obvious
// things.
type Stub struct {
	Module     string
	RylaModule string

	// Name is what the user typed.
	Name string

	Pascal      string
	Camel       string
	Snake       string
	Kebab       string
	Plural      string
	KebabPlural string
	Table       string

	// ID is the full migration identifier, e.g. 20260819120000_create_users.
	ID string
	// Version is the timestamp portion of ID. Templates use it to build
	// identifiers unique to one migration file.
	Version string
}

// NewStub builds a stub for the given entity name.
func NewStub(module, name string) Stub {
	return Stub{
		Module:      module,
		RylaModule:  RylaModule,
		Name:        name,
		Pascal:      naming.Pascal(name),
		Camel:       naming.Camel(name),
		Snake:       naming.Snake(name),
		Kebab:       naming.Kebab(name),
		Plural:      naming.Pluralize(naming.Pascal(name)),
		KebabPlural: naming.Kebab(naming.Pluralize(name)),
		Table:       naming.Table(name),
	}
}

// MigrationVersion formats a timestamp as a migration version.
//
// Second precision is enough: two migrations created in the same second would
// collide, but they would also be indistinguishable in intent, and the registry
// panics on a duplicate rather than silently skipping one.
func MigrationVersion(t time.Time) string {
	return t.UTC().Format("20060102150405")
}

// WithMigration returns a copy of the stub carrying migration identifiers.
// description is the snake_case action, e.g. "create_users".
func (s Stub) WithMigration(description string, t time.Time) Stub {
	s.Version = MigrationVersion(t)
	s.ID = s.Version + "_" + naming.Snake(description)
	return s
}
