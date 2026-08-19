package migrate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	// A file-scoped shared-cache in-memory database, unique per test, so the
	// connection pool sees one database rather than one per connection.
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	return gdb
}

func testRunner(t *testing.T) *Runner {
	t.Helper()
	reset()
	t.Cleanup(reset)
	return &Runner{DB: testDB(t), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

type widget struct {
	ID   uint
	Name string
}

func TestUpAppliesAndIsIdempotent(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()

	Register("20260101000000_create_widgets",
		func(tx *gorm.DB) error { return tx.AutoMigrate(&widget{}) },
		func(tx *gorm.DB) error { return tx.Migrator().DropTable(&widget{}) },
	)

	if err := r.Up(ctx, 0); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if !r.DB.Migrator().HasTable(&widget{}) {
		t.Fatal("widgets table was not created")
	}

	// Running again must be a no-op, not a duplicate-key error.
	if err := r.Up(ctx, 0); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	var count int64
	if err := r.DB.Model(&record{}).Count(&count).Error; err != nil {
		t.Fatalf("count records: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", count)
	}
}

func TestRollbackUndoesLastBatchOnly(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()

	Register("20260101000000_a",
		func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE a (id integer)").Error },
		func(tx *gorm.DB) error { return tx.Exec("DROP TABLE a").Error },
	)
	if err := r.Up(ctx, 0); err != nil {
		t.Fatalf("Up batch 1: %v", err)
	}

	Register("20260102000000_b",
		func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE b (id integer)").Error },
		func(tx *gorm.DB) error { return tx.Exec("DROP TABLE b").Error },
	)
	Register("20260103000000_c",
		func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE c (id integer)").Error },
		func(tx *gorm.DB) error { return tx.Exec("DROP TABLE c").Error },
	)
	if err := r.Up(ctx, 0); err != nil {
		t.Fatalf("Up batch 2: %v", err)
	}

	if err := r.Rollback(ctx, 1); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	m := r.DB.Migrator()
	if !m.HasTable("a") {
		t.Error("table a was rolled back but belonged to an earlier batch")
	}
	if m.HasTable("b") || m.HasTable("c") {
		t.Error("tables b/c survived rollback of their batch")
	}
}

func TestFailedMigrationIsNotRecorded(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()

	boom := errors.New("boom")
	Register("20260101000000_ok",
		func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE ok (id integer)").Error },
		func(tx *gorm.DB) error { return tx.Exec("DROP TABLE ok").Error },
	)
	Register("20260102000000_bad",
		func(tx *gorm.DB) error { return boom },
		nil,
	)

	err := r.Up(ctx, 0)
	if !errors.Is(err, boom) {
		t.Fatalf("Up error = %v, want it to wrap boom", err)
	}

	statuses, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := map[string]bool{"20260101000000_ok": true, "20260102000000_bad": false}
	for _, s := range statuses {
		if applied, ok := want[s.ID]; ok && s.Applied != applied {
			t.Errorf("%s applied = %v, want %v", s.ID, s.Applied, applied)
		}
	}
}

func TestStatusFlagsMigrationsMissingFromCode(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()

	Register("20260101000000_a",
		func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE a (id integer)").Error },
		func(tx *gorm.DB) error { return tx.Exec("DROP TABLE a").Error },
	)
	if err := r.Up(ctx, 0); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Simulate a migration file that was deleted or lives on another branch.
	reset()

	statuses, err := r.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if !statuses[0].Missing {
		t.Error("migration recorded in the database but absent from code was not flagged Missing")
	}
}

func TestUpSteps(t *testing.T) {
	r := testRunner(t)
	ctx := context.Background()

	for _, id := range []string{"20260101000000_a", "20260102000000_b", "20260103000000_c"} {
		name := id[15:]
		Register(id,
			func(tx *gorm.DB) error { return tx.Exec("CREATE TABLE " + name + " (id integer)").Error },
			func(tx *gorm.DB) error { return tx.Exec("DROP TABLE " + name).Error },
		)
	}

	if err := r.Up(ctx, 2); err != nil {
		t.Fatalf("Up(2): %v", err)
	}

	m := r.DB.Migrator()
	if !m.HasTable("a") || !m.HasTable("b") {
		t.Error("Up(2) did not apply the first two migrations")
	}
	if m.HasTable("c") {
		t.Error("Up(2) applied a third migration")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	reset()
	t.Cleanup(reset)

	defer func() {
		if recover() == nil {
			t.Fatal("registering a duplicate migration ID did not panic")
		}
	}()

	up := func(tx *gorm.DB) error { return nil }
	Register("20260101000000_a", up, nil)
	Register("20260101000000_a", up, nil)
}
