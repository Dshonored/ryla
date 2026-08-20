// Package testing gives a generated Ryla application a way to test itself.
//
// It provides the three things an application test needs and none of which are
// worth rewriting per project: a throwaway database with the migrations
// applied, a client that drives the application's real router and middleware
// stack, and assertions that show the response body when they fail.
//
// Requests go through the router the application actually serves, not a
// hand-assembled handler. That is the point: a test that calls a handler
// function directly proves the handler works and proves nothing about the
// session, the CSRF check or the middleware order, which is where the
// interesting failures live.
//
// Import it from _test.go files only:
//
//	rytest "github.com/Dshonored/ryla/testing"
package testing

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Dshonored/ryla/migrate"

	// Registers the "sqlite" dialect with the db package.
	//
	// A project generated for Postgres or MySQL links only its own driver, so
	// without this Env could point DB_DRIVER at SQLite and the application
	// would refuse to open it. Carrying a second driver in a test binary costs
	// nothing, and the alternative is every test needing a database server.
	_ "github.com/Dshonored/ryla/db/sqlite"
)

// TB is the part of *testing.T these helpers use.
//
// It is declared here rather than taking a *testing.T so that the framework
// never imports the standard library's testing package. Importing it registers
// the -test.* flags from an init function in every binary that links it, and a
// framework package has no business doing that to an application.
type TB interface {
	Helper()
	Cleanup(func())
	Setenv(key, value string)
	Name() string
	Logf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Key signs cookies during a test.
//
// It is a fixed, published value on purpose: signatures made with it are
// worthless, so nothing produced by a test can ever be mistaken for something
// a real deployment issued.
const Key = "ryla-testing-key"

// Env points the process environment at a throwaway in-memory SQLite database
// and the rest of a configuration that is safe to run tests against, and
// returns the DSN it chose.
//
// Every setting is written with Setenv, so the test framework restores the
// previous value afterwards and one test cannot leak configuration into the
// next. Real environment variables beat .env, which is what lets this override
// whatever the project's own file says.
//
// A document-store project reads MONGO_URI rather than DB_DSN and simply
// ignores the SQL settings; set that variable after calling this.
func Env(t TB) string {
	t.Helper()

	dsn := DSN(t)

	for key, value := range map[string]string{
		"APP_ENV": "test",
		// Debug on: a panic then renders its stack into the response body,
		// which an assertion failure prints. Hiding it would turn every
		// mistake in a handler into an unexplained 500.
		"APP_DEBUG": "true",
		"APP_KEY":   Key,
		// Plain HTTP, so cookies are not marked Secure. A Secure cookie is
		// never sent back over the recorded http:// requests below, and the
		// session would appear to vanish between them.
		"APP_URL": "http://localhost",

		"DB_DRIVER": "sqlite",
		"DB_DSN":    dsn,
		// One connection, so concurrent writers cannot meet on a shared-cache
		// in-memory database and fail with "database is locked".
		"DB_MAX_OPEN_CONNS": "1",
		"DB_MAX_IDLE_CONNS": "1",
		"DB_LOG_QUERIES":    "false",

		"SESSION_DRIVER": "db",
		"CACHE_DRIVER":   "memory",
		"QUEUE_DRIVER":   "db",

		"MAIL_DRIVER": "log",
		// Inline rather than queued: a test that sends mail should not also
		// have to run a worker before it can see anything happen.
		"MAIL_QUEUE": "false",

		"LOG_LEVEL":  "error",
		"LOG_FORMAT": "text",
		"NO_COLOR":   "1",
	} {
		t.Setenv(key, value)
	}

	return dsn
}

// dbCounter makes every in-memory database unique.
//
// cache=shared means a DSN names one database for the whole process, so reusing
// the test's name reuses its data. That is invisible on a single run and breaks
// immediately under `go test -count=2`, where the second iteration inherits
// whatever the first left behind.
var dbCounter atomic.Int64

// DSN returns a fresh in-memory SQLite DSN, unique to this call.
//
// The pragmas match what a generated project uses on disk, so a foreign key the
// application relies on is enforced in tests too rather than only in production.
func DSN(t TB) string {
	t.Helper()
	return fmt.Sprintf(
		"file:%s-%d?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		safeName(t.Name()), dbCounter.Add(1),
	)
}

// safeName reduces a test name to something a DSN can carry. Subtests are named
// with slashes, which a file: URI would read as a path.
func safeName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, name)
}

// OpenSQLite opens a throwaway in-memory database directly, for a test that
// wants a *gorm.DB without building the whole application around it.
//
// The connection is closed when the test ends, which is also what discards the
// database: an in-memory SQLite lives exactly as long as something is connected
// to it.
func OpenSQLite(t TB) *gorm.DB {
	t.Helper()

	gdb, err := gorm.Open(sqlite.Open(DSN(t)), &gorm.Config{
		Logger:                 gormlogger.Discard,
		SkipDefaultTransaction: true,
		TranslateError:         true,
	})
	if err != nil {
		t.Fatalf("testing: open in-memory database: %v", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("testing: connection pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	return gdb
}

// Migrate applies every migration the application has registered.
//
// It runs the project's real migrations rather than AutoMigrate, so a test
// database has the schema the production one will have — including anything a
// migration does that a model's struct tags do not describe.
func Migrate(t TB, gdb *gorm.DB) {
	t.Helper()

	if gdb == nil {
		t.Fatalf("testing: Migrate was given a nil database")
	}

	runner := &migrate.Runner{
		DB: gdb,
		// Discarded rather than left to slog.Default: the migration log is
		// noise on a passing test, and a failing one reports through the
		// returned error anyway.
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("testing: apply migrations: %v", err)
	}
}
