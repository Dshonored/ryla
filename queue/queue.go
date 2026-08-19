// Package queue runs work outside the request that asked for it.
//
// A job is an ordinary struct: its fields are the payload, serialised to JSON
// when it is pushed and restored before it runs. Jobs survive a restart because
// they live in the database, not in memory.
//
// The point is latency and reliability. Sending a welcome email inside the
// registration handler makes signup as slow as the mail server and fails the
// registration if the mail server is down; neither is acceptable, and neither
// is the caller's problem.
package queue

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Default is the queue name used when none is given.
const Default = "default"

// Job is a unit of background work.
//
// The struct's exported fields are its payload, so keep them serialisable and
// keep them small: store a user's id, not the whole user. By the time the job
// runs the row may have changed, and reading it fresh is almost always what you
// want.
type Job interface {
	// Name identifies the job type. It is written into the database, so
	// changing it orphans jobs already queued under the old name.
	Name() string
	// Run performs the work. Returning an error schedules a retry until the
	// attempt limit is reached.
	Run(ctx context.Context) error
}

// Retryable lets a job override the default retry policy.
type Retryable interface {
	// MaxAttempts is the total number of tries, including the first.
	MaxAttempts() int
}

// BackoffStrategy lets a job override the delay between attempts.
type BackoffStrategy interface {
	// Backoff is how long to wait before attempt n, counting from 1.
	Backoff(attempt int) time.Duration
}

// Queued lets a job choose which queue it runs on, so slow work cannot starve
// quick work.
type Queued interface {
	Queue() string
}

// DefaultMaxAttempts applies to jobs that do not implement Retryable.
const DefaultMaxAttempts = 3

// DefaultBackoff is exponential with a ceiling: 10s, 40s, 90s, 160s... capped
// at ten minutes. Retrying a failing dependency every second only adds load to
// something already struggling.
func DefaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(attempt*attempt) * 10 * time.Second
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

var (
	mu       sync.RWMutex
	registry = map[string]func() Job{}
)

// Register makes a job type runnable by a worker.
//
// The factory returns a zero value of the job; the worker fills its fields from
// the stored payload. Registration happens from init in the package that
// defines the job, so importing that package is enough.
func Register(factory func() Job) {
	if factory == nil {
		panic("queue: Register called with a nil factory")
	}

	sample := factory()
	if sample == nil {
		panic("queue: factory returned nil")
	}
	name := sample.Name()
	if name == "" {
		panic(fmt.Sprintf("queue: %T returned an empty Name", sample))
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[name]; exists {
		// Two jobs sharing a name means one of them silently never runs.
		panic(fmt.Sprintf("queue: duplicate job name %q", name))
	}
	registry[name] = factory
}

// ErrUnknownJob means the stored name has no registered factory — usually a job
// queued by a newer deploy, or one whose package is no longer imported.
var ErrUnknownJob = errors.New("queue: unknown job")

// New builds a zero job by name.
func New(name string) (Job, error) {
	mu.RLock()
	factory, ok := registry[name]
	mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownJob, name)
	}
	return factory(), nil
}

// Registered lists the known job names, sorted.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// reset clears the registry. Tests only.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]func() Job{}
}

// maxAttemptsOf returns a job's attempt limit.
func maxAttemptsOf(j Job) int {
	if r, ok := j.(Retryable); ok {
		if n := r.MaxAttempts(); n > 0 {
			return n
		}
	}
	return DefaultMaxAttempts
}

// backoffOf returns the delay before the given attempt.
func backoffOf(j Job, attempt int) time.Duration {
	if b, ok := j.(BackoffStrategy); ok {
		return b.Backoff(attempt)
	}
	return DefaultBackoff(attempt)
}

// queueOf returns the queue a job belongs on.
func queueOf(j Job) string {
	if q, ok := j.(Queued); ok {
		if name := q.Queue(); name != "" {
			return name
		}
	}
	return Default
}
