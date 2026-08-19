package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Runner applies and rolls back migrations against a database.
type Runner struct {
	DB  *gorm.DB
	Log *slog.Logger
}

// Status describes one migration's state.
type Status struct {
	ID        string
	Applied   bool
	Batch     int
	AppliedAt time.Time
	// Missing is true when the database records a migration that no longer
	// exists in the code — usually a deleted file or a branch switch.
	Missing bool
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// ensureTable creates schema_migrations if it does not exist.
func (r *Runner) ensureTable() error {
	if err := r.DB.AutoMigrate(&record{}); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

func (r *Runner) applied() (map[string]record, int, error) {
	var rows []record
	if err := r.DB.Order("id asc").Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}

	byID := make(map[string]record, len(rows))
	maxBatch := 0
	for _, row := range rows {
		byID[row.ID] = row
		if row.Batch > maxBatch {
			maxBatch = row.Batch
		}
	}
	return byID, maxBatch, nil
}

// Up applies pending migrations in ID order. steps limits how many run; 0 or
// less runs all of them. Every migration applied by a single Up call shares a
// batch number, so Rollback can undo them as a unit.
func (r *Runner) Up(ctx context.Context, steps int) error {
	if err := r.ensureTable(); err != nil {
		return err
	}

	done, maxBatch, err := r.applied()
	if err != nil {
		return err
	}

	all := Registered()
	var pending []Migration
	for _, m := range all {
		if _, ok := done[m.ID]; !ok {
			pending = append(pending, m)
		}
	}

	if len(pending) == 0 {
		r.logger().Info("migrations: nothing to migrate")
		return nil
	}

	// Warn about a migration that sorts before something already applied.
	// This happens when two branches add migrations and one merges late; the
	// migration still runs, but the schema it lands on is not the schema it was
	// written against, and that is worth knowing before it fails.
	var highestApplied string
	for id := range done {
		if id > highestApplied {
			highestApplied = id
		}
	}
	for _, m := range pending {
		if highestApplied != "" && m.ID < highestApplied {
			r.logger().Warn("migrations: out-of-order migration",
				"migration", m.ID,
				"already_applied", highestApplied,
			)
		}
	}

	if steps > 0 && steps < len(pending) {
		pending = pending[:steps]
	}

	batch := maxBatch + 1
	for _, m := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}

		start := time.Now()
		r.logger().Info("migrating", "migration", m.ID)

		// Each migration gets its own transaction, so a failure leaves every
		// earlier migration applied and this one fully rolled back. Note that
		// MySQL commits implicitly on DDL, so there the rollback is partial —
		// SQLite and Postgres have transactional DDL and behave as written.
		err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Up(tx); err != nil {
				return err
			}
			return tx.Create(&record{ID: m.ID, Batch: batch, AppliedAt: time.Now().UTC()}).Error
		})
		if err != nil {
			return fmt.Errorf("migrate: %s: %w", m.ID, err)
		}

		r.logger().Info("migrated",
			"migration", m.ID,
			"batch", batch,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		)
	}
	return nil
}

// Rollback reverses the most recent batches. batches defaults to 1.
func (r *Runner) Rollback(ctx context.Context, batches int) error {
	if err := r.ensureTable(); err != nil {
		return err
	}
	if batches <= 0 {
		batches = 1
	}

	done, maxBatch, err := r.applied()
	if err != nil {
		return err
	}
	if maxBatch == 0 {
		r.logger().Info("migrations: nothing to roll back")
		return nil
	}

	lowest := maxBatch - batches + 1
	if lowest < 1 {
		lowest = 1
	}

	var targets []record
	for _, row := range done {
		if row.Batch >= lowest {
			targets = append(targets, row)
		}
	}
	// Reverse ID order: undo the newest change first.
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID > targets[j].ID })

	byID := make(map[string]Migration)
	for _, m := range Registered() {
		byID[m.ID] = m
	}

	for _, row := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}

		m, ok := byID[row.ID]
		if !ok {
			return fmt.Errorf("migrate: cannot roll back %s: migration is not in the code", row.ID)
		}
		if m.Down == nil {
			return fmt.Errorf("migrate: cannot roll back %s: no Down function", row.ID)
		}

		r.logger().Info("rolling back", "migration", m.ID, "batch", row.Batch)

		err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Down(tx); err != nil {
				return err
			}
			return tx.Delete(&record{ID: m.ID}).Error
		})
		if err != nil {
			return fmt.Errorf("migrate: rollback %s: %w", m.ID, err)
		}
	}
	return nil
}

// Reset rolls back every applied migration.
func (r *Runner) Reset(ctx context.Context) error {
	if err := r.ensureTable(); err != nil {
		return err
	}
	_, maxBatch, err := r.applied()
	if err != nil {
		return err
	}
	return r.Rollback(ctx, maxBatch)
}

// Refresh rolls everything back and re-applies it.
func (r *Runner) Refresh(ctx context.Context) error {
	if err := r.Reset(ctx); err != nil {
		return err
	}
	return r.Up(ctx, 0)
}

// Status reports the state of every known migration, including any recorded in
// the database but absent from the code.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	if err := r.ensureTable(); err != nil {
		return nil, err
	}
	done, _, err := r.applied()
	if err != nil {
		return nil, err
	}

	var out []Status
	seen := map[string]bool{}
	for _, m := range Registered() {
		seen[m.ID] = true
		if row, ok := done[m.ID]; ok {
			out = append(out, Status{ID: m.ID, Applied: true, Batch: row.Batch, AppliedAt: row.AppliedAt})
			continue
		}
		out = append(out, Status{ID: m.ID})
	}
	for id, row := range done {
		if !seen[id] {
			out = append(out, Status{ID: id, Applied: true, Batch: row.Batch, AppliedAt: row.AppliedAt, Missing: true})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
