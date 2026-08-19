package queue

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ReservationTimeout is how long a claimed job may run before another worker
// may take it. A worker killed mid-job leaves its row reserved forever
// otherwise, and the job silently never completes.
const ReservationTimeout = 15 * time.Minute

// DBDriver stores jobs in the application's database.
//
// One table, no extra infrastructure, and jobs survive a restart because they
// were never only in memory. It is not built for tens of thousands of jobs a
// second — reach for Redis at that point — but it is more than enough for the
// email, webhook and report work most applications actually queue.
type DBDriver struct {
	DB *gorm.DB
	// Timeout overrides ReservationTimeout.
	Timeout time.Duration
}

// NewDBDriver builds a database-backed driver.
func NewDBDriver(db *gorm.DB) *DBDriver { return &DBDriver{DB: db} }

func (d *DBDriver) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return ReservationTimeout
}

// Push implements Driver.
func (d *DBDriver) Push(ctx context.Context, rec *Record) error {
	return d.DB.WithContext(ctx).Create(rec).Error
}

// Claim implements Driver.
//
// The select and the reservation happen inside one transaction, and the update
// re-checks that the row is still unreserved. Two workers racing for the same
// job therefore produce one winner and one empty result, rather than the job
// running twice.
func (d *DBDriver) Claim(ctx context.Context, queue string) (*Record, error) {
	var claimed *Record

	err := d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		staleBefore := now.Add(-d.timeout())

		var rec Record
		err := tx.
			Where("queue = ?", queue).
			Where("available_at <= ?", now).
			Where("reserved_at IS NULL OR reserved_at < ?", staleBefore).
			Order("available_at asc, id asc").
			First(&rec).Error

		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}

		// The guard in the WHERE clause is what makes this safe without row
		// locks: if another worker reserved the row between the select and
		// here, no rows are affected and this worker takes nothing.
		res := tx.Model(&Record{}).
			Where("id = ?", rec.ID).
			Where("reserved_at IS NULL OR reserved_at < ?", staleBefore).
			Updates(map[string]any{
				"reserved_at": now,
				"attempts":    rec.Attempts + 1,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}

		rec.ReservedAt = &now
		rec.Attempts++
		claimed = &rec
		return nil
	})

	return claimed, err
}

// Complete implements Driver.
func (d *DBDriver) Complete(ctx context.Context, rec *Record) error {
	return d.DB.WithContext(ctx).Delete(&Record{}, rec.ID).Error
}

// Release implements Driver.
func (d *DBDriver) Release(ctx context.Context, rec *Record, at time.Time, reason string) error {
	return d.DB.WithContext(ctx).Model(&Record{}).
		Where("id = ?", rec.ID).
		Updates(map[string]any{
			"reserved_at":  nil,
			"available_at": at,
			"last_error":   truncate(reason, 4000),
		}).Error
}

// Fail implements Driver.
//
// The move and the delete share a transaction, so a job can never be both
// pending and failed, nor vanish between the two.
func (d *DBDriver) Fail(ctx context.Context, rec *Record, reason string) error {
	return d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		failed := FailedRecord{
			Queue:     rec.Queue,
			Name:      rec.Name,
			Payload:   rec.Payload,
			Attempts:  rec.Attempts,
			LastError: truncate(reason, 4000),
			FailedAt:  time.Now(),
		}
		if err := tx.Create(&failed).Error; err != nil {
			return err
		}
		return tx.Delete(&Record{}, rec.ID).Error
	})
}

// Pending implements Driver.
func (d *DBDriver) Pending(ctx context.Context, queue string) (int64, error) {
	var n int64
	err := d.DB.WithContext(ctx).Model(&Record{}).Where("queue = ?", queue).Count(&n).Error
	return n, err
}

// Failed returns jobs that exhausted their attempts, newest first.
func (d *DBDriver) Failed(ctx context.Context, limit int) ([]FailedRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []FailedRecord
	err := d.DB.WithContext(ctx).Order("failed_at desc").Limit(limit).Find(&out).Error
	return out, err
}

// Retry moves a failed job back onto its queue, with its attempt count reset.
func (d *DBDriver) Retry(ctx context.Context, id uint) error {
	return d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var failed FailedRecord
		if err := tx.First(&failed, id).Error; err != nil {
			return err
		}

		rec := Record{
			Queue:       failed.Queue,
			Name:        failed.Name,
			Payload:     failed.Payload,
			MaxAttempts: DefaultMaxAttempts,
			AvailableAt: time.Now(),
			CreatedAt:   time.Now(),
		}
		if err := tx.Create(&rec).Error; err != nil {
			return err
		}
		return tx.Delete(&FailedRecord{}, id).Error
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
