// Package migrate provides versioned, code-first database migrations.
//
// A migration is a Go file that registers itself from init:
//
//	func init() {
//	    migrate.Register("20260819120000_create_users", up, down)
//	}
//
// The application blank-imports its database/migrations package, so every
// migration in the tree is registered automatically and the compiler checks
// each one. Migrations use GORM's schema builder rather than raw SQL, which is
// what keeps them portable across SQLite, Postgres and MySQL.
package migrate

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Func is the body of a migration direction.
type Func func(tx *gorm.DB) error

// Migration is a single registered schema change.
type Migration struct {
	// ID is the timestamped, sortable identifier, e.g.
	// "20260819120000_create_users". It must be unique and must never change
	// once the migration has run anywhere.
	ID   string
	Up   Func
	Down Func
}

var (
	mu         sync.Mutex
	registered = map[string]Migration{}
)

// Register adds a migration to the global registry. It panics on a duplicate
// ID, because two migrations sharing an ID means one of them will silently
// never run.
func Register(id string, up, down Func) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := registered[id]; exists {
		panic(fmt.Sprintf("migrate: duplicate migration ID %q", id))
	}
	if up == nil {
		panic(fmt.Sprintf("migrate: migration %q has no Up function", id))
	}
	registered[id] = Migration{ID: id, Up: up, Down: down}
}

// Registered returns every registered migration, sorted by ID.
func Registered() []Migration {
	mu.Lock()
	defer mu.Unlock()

	out := make([]Migration, 0, len(registered))
	for _, m := range registered {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// reset clears the registry. Tests only.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	registered = map[string]Migration{}
}

// record is the schema_migrations row tracking what has been applied.
type record struct {
	ID        string    `gorm:"column:id;primaryKey;size:255"`
	Batch     int       `gorm:"column:batch;index"`
	AppliedAt time.Time `gorm:"column:applied_at"`
}

// TableName pins the table name so GORM's pluralisation can't surprise us.
func (record) TableName() string { return "schema_migrations" }
