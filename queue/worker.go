package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// Worker runs jobs from a queue until its context is cancelled.
type Worker struct {
	Driver Driver
	Log    *slog.Logger

	// Queue is the queue to drain. Defaults to "default".
	Queue string
	// Concurrency is how many jobs may run at once. Defaults to 1, which keeps
	// ordering predictable and is the right starting point for most work.
	Concurrency int
	// PollInterval is how long to wait after finding nothing. Defaults to one
	// second.
	PollInterval time.Duration
	// ShutdownTimeout bounds how long Run waits for in-flight jobs after the
	// context is cancelled.
	ShutdownTimeout time.Duration
}

// Run drains the queue until ctx is cancelled, then waits for in-flight jobs.
//
// A job already running is allowed to finish rather than being killed: it has
// usually done something irreversible by now — sent the email, charged the card
// — and cutting it off midway is worse than waiting.
func (w *Worker) Run(ctx context.Context) error {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}

	queue := w.Queue
	if queue == "" {
		queue = Default
	}
	concurrency := w.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	poll := w.PollInterval
	if poll <= 0 {
		poll = time.Second
	}

	log.Info("queue worker started",
		"queue", queue, "concurrency", concurrency, "jobs", len(Registered()))

	// A buffered channel as a semaphore: it bounds concurrency and, once
	// drained, proves every job has finished.
	slots := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			log.Info("queue worker stopping, waiting for in-flight jobs")
			if w.wait(&wg) {
				log.Info("queue worker stopped")
			} else {
				log.Warn("queue worker stopped with jobs still running")
			}
			return nil
		default:
		}

		rec, err := w.Driver.Claim(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			// A database blip should not kill the worker; back off and retry.
			log.Error("could not claim a job", "error", err)
			w.sleep(ctx, poll)
			continue
		}

		if rec == nil {
			w.sleep(ctx, poll)
			continue
		}

		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			// Shutting down before this job could start: hand it back so it is
			// not stuck reserved until the timeout.
			_ = w.Driver.Release(context.WithoutCancel(ctx), rec, time.Now(), "worker shutting down")
			continue
		}

		wg.Add(1)
		go func(rec *Record) {
			defer wg.Done()
			defer func() { <-slots }()

			// Detached from ctx so a job that has already started completes
			// during shutdown rather than being cancelled halfway.
			w.process(context.WithoutCancel(ctx), log, rec)
		}(rec)
	}
}

// wait blocks for in-flight jobs, returning false if the timeout expires first.
func (w *Worker) wait(wg *sync.WaitGroup) bool {
	timeout := w.ShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (w *Worker) sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// process runs one job and records the outcome.
func (w *Worker) process(ctx context.Context, log *slog.Logger, rec *Record) {
	log = log.With("job", rec.Name, "id", rec.ID, "attempt", rec.Attempts)

	job, err := decode(rec)
	if err != nil {
		// An unknown or undecodable job will never succeed, however many times
		// it is retried, so it fails immediately.
		log.Error("could not decode job", "error", err)
		if failErr := w.Driver.Fail(ctx, rec, err.Error()); failErr != nil {
			log.Error("could not record the failure", "error", failErr)
		}
		return
	}

	start := time.Now()
	err = w.run(ctx, job)
	took := time.Since(start).Round(time.Millisecond)

	if err == nil {
		if err := w.Driver.Complete(ctx, rec); err != nil {
			// The job did its work; failing to delete the row means it runs
			// again, which is why jobs must tolerate being run twice.
			log.Error("job succeeded but could not be removed from the queue", "error", err)
			return
		}
		log.Info("job done", "duration", took.String())
		return
	}

	if rec.Attempts >= rec.MaxAttempts {
		log.Error("job failed permanently", "error", err, "attempts", rec.Attempts, "duration", took.String())
		if failErr := w.Driver.Fail(ctx, rec, err.Error()); failErr != nil {
			log.Error("could not record the failure", "error", failErr)
		}
		return
	}

	retryAt := time.Now().Add(backoffOf(job, rec.Attempts))
	log.Warn("job failed, will retry", "error", err, "retry_at", retryAt.Format(time.RFC3339), "duration", took.String())

	if relErr := w.Driver.Release(ctx, rec, retryAt, err.Error()); relErr != nil {
		log.Error("could not reschedule the job", "error", relErr)
	}
}

// run executes a job, turning a panic into an error.
//
// A panicking job would otherwise take the whole worker process down and stop
// every other job with it.
func (w *Worker) run(ctx context.Context, job Job) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v\n%s", rec, debug.Stack())
		}
	}()
	return job.Run(ctx)
}

// ErrNoDriver is returned when a worker has nothing to read from.
var ErrNoDriver = errors.New("queue: worker has no driver")
