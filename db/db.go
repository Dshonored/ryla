// Package db opens and configures the application's GORM connection.
package db

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config describes a database connection. Driver selects the dialect; DSN is
// dialect-specific and comes from the application's config.
type Config struct {
	Driver string
	DSN    string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration

	// SlowThreshold logs any query slower than this. Zero disables it.
	SlowThreshold time.Duration
	// LogQueries echoes every statement. Useful locally, noisy in production.
	LogQueries bool
}

// Dialector builds a GORM dialector for a DSN. Each supported database
// registers one, which is what keeps `ry new --db=postgres` a template overlay
// rather than a change to this package.
type Dialector func(dsn string) gorm.Dialector

var dialectors = map[string]Dialector{}

// Register makes a driver available to Open. Drivers register themselves from
// an init function in their own file, so an application only links the database
// driver it actually uses.
func Register(driver string, d Dialector) { dialectors[driver] = d }

// Drivers lists the registered driver names.
func Drivers() []string {
	out := make([]string, 0, len(dialectors))
	for name := range dialectors {
		out = append(out, name)
	}
	return out
}

// Open connects to the database and applies pool settings.
func Open(cfg Config, log *slog.Logger) (*gorm.DB, error) {
	dialector, ok := dialectors[cfg.Driver]
	if !ok {
		return nil, fmt.Errorf("db: unknown driver %q (registered: %v)", cfg.Driver, Drivers())
	}

	gdb, err := gorm.Open(dialector(cfg.DSN), &gorm.Config{
		Logger:                 newLogger(log, cfg),
		SkipDefaultTransaction: true,
		TranslateError:         true,
	})
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", cfg.Driver, err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: pool: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	return gdb, nil
}

// Close releases the underlying connection pool.
func Close(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Transaction runs fn inside a database transaction, rolling back on error or
// panic and committing otherwise.
func Transaction(gdb *gorm.DB, fn func(tx *gorm.DB) error) error {
	return gdb.Transaction(fn)
}

func newLogger(log *slog.Logger, cfg Config) gormlogger.Interface {
	level := gormlogger.Warn
	if cfg.LogQueries {
		level = gormlogger.Info
	}
	return gormlogger.New(slogWriter{log}, gormlogger.Config{
		SlowThreshold:             cfg.SlowThreshold,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// slogWriter adapts slog to the Printf-style writer GORM's logger expects.
type slogWriter struct{ log *slog.Logger }

func (w slogWriter) Printf(format string, args ...any) {
	if w.log == nil {
		slog.Info(fmt.Sprintf(format, args...))
		return
	}
	w.log.Info(fmt.Sprintf(format, args...))
}
