// Package schedule runs work on a recurring timetable.
//
// Tasks are declared in code rather than in a crontab, so they are reviewed,
// versioned and deployed with everything else — and a task that references a
// function which no longer exists fails to compile instead of failing silently
// at three in the morning.
//
// The scheduler runs inside the application binary, so a deployment carries its
// own timetable and there is no separate machine to keep in step.
package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// Task is one scheduled piece of work.
type Task struct {
	// Name identifies the task in logs and in the overlap guard.
	Name string
	// Every is how often it runs.
	Every time.Duration
	// At pins the time of day for a daily task, as "15:04". Ignored unless
	// Every is a multiple of 24 hours.
	At string
	// Run does the work.
	Run func(ctx context.Context) error
	// AllowOverlap lets a run start while the previous one is still going. Off
	// by default: a task that takes longer than its interval would otherwise
	// pile up copies of itself until the process falls over.
	AllowOverlap bool

	mu      sync.Mutex
	running bool
	last    time.Time
}

// Scheduler owns a set of tasks.
type Scheduler struct {
	Log *slog.Logger

	mu    sync.Mutex
	tasks []*Task
}

// New builds a scheduler.
func New(log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{Log: log}
}

// Add registers a task.
func (s *Scheduler) Add(t *Task) {
	if t.Name == "" {
		panic("schedule: task has no name")
	}
	if t.Run == nil {
		panic(fmt.Sprintf("schedule: task %q has nothing to run", t.Name))
	}
	if t.Every <= 0 {
		panic(fmt.Sprintf("schedule: task %q has no interval", t.Name))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.tasks {
		if existing.Name == t.Name {
			panic(fmt.Sprintf("schedule: duplicate task name %q", t.Name))
		}
	}
	s.tasks = append(s.tasks, t)
}

// Every registers a task that runs on an interval.
func (s *Scheduler) Every(every time.Duration, name string, run func(context.Context) error) *Task {
	t := &Task{Name: name, Every: every, Run: run}
	s.Add(t)
	return t
}

// Daily registers a task that runs once a day at a given time, written "15:04".
func (s *Scheduler) Daily(at, name string, run func(context.Context) error) *Task {
	t := &Task{Name: name, Every: 24 * time.Hour, At: at, Run: run}
	s.Add(t)
	return t
}

// Tasks lists the registered tasks, sorted by name.
func (s *Scheduler) Tasks() []*Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Task, len(s.tasks))
	copy(out, s.tasks)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Run ticks until ctx is cancelled.
//
// The tick is deliberately much finer than any task's interval: due times are
// checked against the clock rather than counted, so a task scheduled for 03:00
// still runs at 03:00 after the process restarts at 02:59.
func (s *Scheduler) Run(ctx context.Context, tick time.Duration) error {
	if tick <= 0 {
		tick = 30 * time.Second
	}

	s.Log.Info("scheduler started", "tasks", len(s.Tasks()), "tick", tick.String())

	// Seed the last-run times so nothing fires the instant the process starts
	// — a deploy should not trigger every daily job.
	now := time.Now()
	for _, t := range s.Tasks() {
		t.mu.Lock()
		t.last = now
		t.mu.Unlock()
	}

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			s.Log.Info("scheduler stopping, waiting for running tasks")
			wg.Wait()
			return nil

		case now := <-ticker.C:
			for _, t := range s.Tasks() {
				if !t.due(now) {
					continue
				}

				wg.Add(1)
				go func(t *Task) {
					defer wg.Done()
					// Detached from ctx so a task already started finishes
					// during shutdown rather than being cancelled halfway.
					s.run(context.WithoutCancel(ctx), t)
				}(t)
			}
		}
	}
}

// RunOnce runs every task that is due right now and returns. This is what a
// system cron would call if you would rather not keep a process running.
func (s *Scheduler) RunOnce(ctx context.Context) error {
	now := time.Now()

	var wg sync.WaitGroup
	for _, t := range s.Tasks() {
		t.mu.Lock()
		t.last = time.Time{} // never run, from this process's point of view
		t.mu.Unlock()

		if !t.due(now) {
			continue
		}

		wg.Add(1)
		go func(t *Task) {
			defer wg.Done()
			s.run(ctx, t)
		}(t)
	}
	wg.Wait()
	return nil
}

// due reports whether a task should run now.
func (t *Task) due(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running && !t.AllowOverlap {
		// A task running longer than its interval would otherwise pile up
		// copies of itself until the process falls over.
		return false
	}

	if t.At != "" && t.Every >= 24*time.Hour {
		return t.dueAtTimeOfDay(now)
	}
	return t.last.IsZero() || now.Sub(t.last) >= t.Every
}

// dueAtTimeOfDay handles a task pinned to a clock time.
func (t *Task) dueAtTimeOfDay(now time.Time) bool {
	hour, minute, ok := parseAt(t.At)
	if !ok {
		return false
	}

	target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if now.Before(target) {
		return false
	}
	// Already run since the target passed today.
	return t.last.Before(target)
}

func parseAt(at string) (hour, minute int, ok bool) {
	parsed, err := time.Parse("15:04", at)
	if err != nil {
		return 0, 0, false
	}
	return parsed.Hour(), parsed.Minute(), true
}

// run executes a task, recording the outcome.
func (s *Scheduler) run(ctx context.Context, t *Task) {
	t.mu.Lock()
	if t.running && !t.AllowOverlap {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.last = time.Now()
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
	}()

	log := s.Log.With("task", t.Name)
	start := time.Now()

	if err := safely(ctx, t.Run); err != nil {
		log.Error("scheduled task failed", "error", err,
			"duration", time.Since(start).Round(time.Millisecond).String())
		return
	}
	log.Info("scheduled task done", "duration", time.Since(start).Round(time.Millisecond).String())
}

// safely turns a panic into an error, so one bad task cannot take the whole
// scheduler — and with it every other task — down.
func safely(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v\n%s", rec, debug.Stack())
		}
	}()
	return run(ctx)
}
