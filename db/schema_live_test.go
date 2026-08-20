package db_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/db"

	// Registering the dialects is the point of these tests, so they are
	// imported for the same side effect a generated project's database package
	// relies on.
	_ "github.com/Dshonored/ryla/db/mysql"
	_ "github.com/Dshonored/ryla/db/postgres"
)

// The snapshots below are copies of the tables the scaffolder ships in
// database/migrations, and of the users table make:auth generates. They are
// copied rather than imported because the originals are templates, which is
// exactly why nothing else in the build can tell whether they still work: they
// are text until someone runs `ry new`.
//
// They must stay in step with cmd/ry/internal/templates/db/*/database/migrations
// and cmd/ry/internal/templates/make/migration_users.go.tmpl.

type liveSession struct {
	ID        string    `gorm:"primaryKey;size:64"`
	Data      string    `gorm:"type:text"`
	UserID    string    `gorm:"index;size:64"`
	IP        string    `gorm:"size:45"`
	UserAgent string    `gorm:"size:255"`
	ExpiresAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

func (liveSession) TableName() string { return "sessions" }

type liveJob struct {
	ID          uint       `gorm:"primaryKey"`
	Queue       string     `gorm:"size:64;index:idx_jobs_claim,priority:1;not null"`
	Name        string     `gorm:"size:128;not null"`
	Payload     string     `gorm:"type:text"`
	Attempts    int        `gorm:"not null"`
	MaxAttempts int        `gorm:"not null"`
	AvailableAt time.Time  `gorm:"index:idx_jobs_claim,priority:2;not null"`
	ReservedAt  *time.Time `gorm:"index"`
	LastError   string     `gorm:"type:text"`
	CreatedAt   time.Time
}

func (liveJob) TableName() string { return "jobs" }

type liveFailedJob struct {
	ID        uint      `gorm:"primaryKey"`
	Queue     string    `gorm:"size:64;index"`
	Name      string    `gorm:"size:128"`
	Payload   string    `gorm:"type:text"`
	Attempts  int       `gorm:"not null"`
	LastError string    `gorm:"type:text"`
	FailedAt  time.Time `gorm:"index"`
}

func (liveFailedJob) TableName() string { return "failed_jobs" }

type liveCache struct {
	Key       string     `gorm:"primaryKey;size:255"`
	Value     []byte     ``
	ExpiresAt *time.Time `gorm:"index"`
}

func (liveCache) TableName() string { return "cache" }

type liveUser struct {
	ID              uint   `gorm:"primaryKey"`
	Name            string `gorm:"size:80;not null"`
	Email           string `gorm:"size:255;not null;uniqueIndex"`
	Password        string `gorm:"size:255;not null"`
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (liveUser) TableName() string { return "users" }

// liveSchemas is every table under test, in creation order.
func liveSchemas() []any {
	return []any{&liveSession{}, &liveJob{}, &liveFailedJob{}, &liveCache{}, &liveUser{}}
}

// live connects to a real server, or skips.
//
// There is no in-process Postgres or MySQL the way miniredis stands in for
// Redis, so these run where a server exists — CI provides one — and are skipped
// elsewhere rather than silently not testing anything.
func live(t *testing.T, driver, env string) *gorm.DB {
	t.Helper()

	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("skipping: set %s to run the %s integration tests", env, driver)
	}

	gdb, err := db.Open(db.Config{Driver: driver, DSN: dsn, MaxOpenConns: 4}, nil)
	if err != nil {
		t.Fatalf("connect to %s: %v", driver, err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })

	// A clean slate before and after, so a run that died half way cannot decide
	// what the next one sees.
	dropAll(gdb)
	t.Cleanup(func() { dropAll(gdb) })

	return gdb
}

func dropAll(gdb *gorm.DB) {
	schemas := liveSchemas()
	for i := len(schemas) - 1; i >= 0; i-- {
		_ = gdb.Migrator().DropTable(schemas[i])
	}
}

// TestLivePostgresShippedMigrationsApply checks that every table the scaffolder
// ships can actually be built on Postgres.
//
// The migrations are written against GORM's migrator rather than raw SQL
// specifically so one set of files serves three engines. That is a claim, not a
// guarantee: a tag combination SQLite tolerates can still be rejected here, and
// nothing in the repository's own build would notice.
func TestLivePostgresShippedMigrationsApply(t *testing.T) {
	assertShippedSchemaApplies(t, live(t, "postgres", "POSTGRES_TEST_DSN"))
}

// TestLiveMySQLShippedMigrationsApply is the Postgres test's counterpart, and
// the one more likely to fail: InnoDB caps an index key at 3072 bytes, so the
// unique index on users.email and the primary key on cache.key — 255 utf8mb4
// characters, 1020 bytes each — are the sizes to watch if this ever breaks.
func TestLiveMySQLShippedMigrationsApply(t *testing.T) {
	assertShippedSchemaApplies(t, live(t, "mysql", "MYSQL_TEST_DSN"))
}

func assertShippedSchemaApplies(t *testing.T, gdb *gorm.DB) {
	t.Helper()

	m := gdb.Migrator()
	for _, schema := range liveSchemas() {
		if err := m.CreateTable(schema); err != nil {
			t.Fatalf("create table for %T: %v", schema, err)
		}
	}

	for _, schema := range liveSchemas() {
		if !m.HasTable(schema) {
			t.Errorf("table for %T was not created", schema)
		}
	}

	// The composite index is the reason claiming a job is one index seek rather
	// than a scan of the whole queue. GORM builds it from two priority tags on
	// separate fields, which is the part most likely to render differently
	// between dialects.
	if !m.HasIndex(&liveJob{}, "idx_jobs_claim") {
		t.Error("jobs is missing idx_jobs_claim; claiming a job will scan the table")
	}
	if !m.HasIndex(&liveUser{}, "idx_users_email") {
		t.Error("users is missing its unique index on email; duplicate accounts become possible")
	}

	assertCacheHoldsBinary(t, gdb)
}

// assertCacheHoldsBinary protects the cache column against a dialect mapping
// []byte onto a text type. Cached values are serialised, not printable: a
// column that round-trips text but mangles a zero byte would corrupt entries
// silently, and only for some of them.
func assertCacheHoldsBinary(t *testing.T, gdb *gorm.DB) {
	t.Helper()

	value := []byte{0x00, 0xff, 0x01, 0x80, 0x00, 0x7f}

	// 255 two-byte characters, to confirm the key column is sized in characters
	// rather than bytes and that its index tolerates the widest key it claims
	// to accept.
	key := strings.Repeat("ä", 255)

	if err := gdb.Create(&liveCache{Key: key, Value: value}).Error; err != nil {
		t.Fatalf("write a cache row: %v", err)
	}

	// Matched by struct rather than by a "key = ?" string: key is a reserved
	// word in MySQL, and only the struct form goes through GORM's quoting.
	var got liveCache
	if err := gdb.Where(&liveCache{Key: key}).Take(&got).Error; err != nil {
		t.Fatalf("read the cache row back: %v", err)
	}

	if string(got.Value) != string(value) {
		t.Errorf("cache value round-tripped as % x, want % x", got.Value, value)
	}
	if got.Key != key {
		t.Errorf("cache key round-tripped as %d characters, want %d", len([]rune(got.Key)), 255)
	}
}
