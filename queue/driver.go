package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Record is a queued job as stored.
type Record struct {
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

// TableName pins the table name.
func (Record) TableName() string { return "jobs" }

// FailedRecord is a job that exhausted its attempts.
//
// Failures are kept rather than discarded: a job that gave up is usually the
// first sign of a broken dependency, and being able to inspect and re-queue it
// is the difference between an incident and a lost email.
type FailedRecord struct {
	ID        uint      `gorm:"primaryKey"`
	Queue     string    `gorm:"size:64;index"`
	Name      string    `gorm:"size:128"`
	Payload   string    `gorm:"type:text"`
	Attempts  int       `gorm:"not null"`
	LastError string    `gorm:"type:text"`
	FailedAt  time.Time `gorm:"index"`
}

// TableName pins the table name.
func (FailedRecord) TableName() string { return "failed_jobs" }

// Driver stores and hands out jobs. The database driver is the only one that
// ships; the interface exists so Redis or Postgres-specific queues can slot in
// without touching a single job.
type Driver interface {
	// Push stores a job for later.
	Push(ctx context.Context, rec *Record) error
	// Claim reserves the next due job on a queue, or returns nil when there is
	// nothing to do. It must be safe for several workers at once: exactly one
	// may receive any given job.
	Claim(ctx context.Context, queue string) (*Record, error)
	// Complete removes a finished job.
	Complete(ctx context.Context, rec *Record) error
	// Release puts a failed job back with a later availability time.
	Release(ctx context.Context, rec *Record, at time.Time, reason string) error
	// Fail moves a job that exhausted its attempts to the failed table.
	Fail(ctx context.Context, rec *Record, reason string) error
	// Pending counts jobs waiting on a queue.
	Pending(ctx context.Context, queue string) (int64, error)
}

// Dispatcher pushes jobs onto a queue.
type Dispatcher struct {
	Driver Driver
}

// NewDispatcher builds a dispatcher.
func NewDispatcher(d Driver) *Dispatcher { return &Dispatcher{Driver: d} }

// Dispatch queues a job to run as soon as a worker is free.
func (d *Dispatcher) Dispatch(ctx context.Context, job Job) error {
	return d.DispatchAt(ctx, job, time.Now())
}

// DispatchIn queues a job to run after a delay.
func (d *Dispatcher) DispatchIn(ctx context.Context, job Job, delay time.Duration) error {
	return d.DispatchAt(ctx, job, time.Now().Add(delay))
}

// DispatchAt queues a job to run at a specific time.
//
// The job is serialised now, so it carries the values it had when dispatched
// rather than whatever they become later.
func (d *Dispatcher) DispatchAt(ctx context.Context, job Job, at time.Time) error {
	if job == nil {
		return fmt.Errorf("queue: cannot dispatch a nil job")
	}

	// Fail loudly at dispatch if the job type was never registered. The
	// alternative is a row that no worker can ever run, discovered days later.
	if _, err := New(job.Name()); err != nil {
		return err
	}

	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: encode %s: %w", job.Name(), err)
	}

	return d.Driver.Push(ctx, &Record{
		Queue:       queueOf(job),
		Name:        job.Name(),
		Payload:     string(payload),
		MaxAttempts: maxAttemptsOf(job),
		AvailableAt: at,
		CreatedAt:   time.Now(),
	})
}

// decode rebuilds a job from a stored record.
func decode(rec *Record) (Job, error) {
	job, err := New(rec.Name)
	if err != nil {
		return nil, err
	}
	if rec.Payload == "" {
		return job, nil
	}
	if err := json.Unmarshal([]byte(rec.Payload), job); err != nil {
		return nil, fmt.Errorf("queue: decode %s: %w", rec.Name, err)
	}
	return job, nil
}

// FailedLister is implemented by drivers that keep failed jobs. Both shipped
// drivers do; the interface exists so the app's commands can work with
// whichever one is configured rather than assuming the database.
type FailedLister interface {
	Failed(ctx context.Context, limit int) ([]FailedRecord, error)
}

// Retrier is implemented by drivers that can put a failed job back.
type Retrier interface {
	Retry(ctx context.Context, id uint) error
}
