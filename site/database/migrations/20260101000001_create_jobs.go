package migrations

import (
	"time"

	"gorm.io/gorm"

	"github.com/Dshonored/ryla/migrate"
)

func init() {
	migrate.Register("20260101000001_create_jobs", up20260101000001, down20260101000001)
}

// job20260101000001 is the queue table as of this migration. The composite
// index on (queue, available_at) is what keeps claiming a job a single index
// seek rather than a scan once the table has any depth to it.
type job20260101000001 struct {
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

func (job20260101000001) TableName() string { return "jobs" }

// failedJob20260101000001 keeps jobs that exhausted their attempts. They are
// kept rather than discarded: a job that gave up is usually the first sign of a
// broken dependency, and being able to inspect and re-queue it is the
// difference between an incident and a lost email.
type failedJob20260101000001 struct {
	ID        uint      `gorm:"primaryKey"`
	Queue     string    `gorm:"size:64;index"`
	Name      string    `gorm:"size:128"`
	Payload   string    `gorm:"type:text"`
	Attempts  int       `gorm:"not null"`
	LastError string    `gorm:"type:text"`
	FailedAt  time.Time `gorm:"index"`
}

func (failedJob20260101000001) TableName() string { return "failed_jobs" }

func up20260101000001(tx *gorm.DB) error {
	if err := tx.Migrator().CreateTable(&job20260101000001{}); err != nil {
		return err
	}
	return tx.Migrator().CreateTable(&failedJob20260101000001{})
}

func down20260101000001(tx *gorm.DB) error {
	if err := tx.Migrator().DropTable(&failedJob20260101000001{}); err != nil {
		return err
	}
	return tx.Migrator().DropTable(&job20260101000001{})
}
