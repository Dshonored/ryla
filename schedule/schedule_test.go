package schedule

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEveryRunsOnItsInterval(t *testing.T) {
	s := New(quiet())

	var runs atomic.Int32
	s.Every(30*time.Millisecond, "tick", func(context.Context) error {
		runs.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := s.Run(ctx, 10*time.Millisecond); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Roughly 200ms of a 30ms task, minus the seeded first interval.
	if n := runs.Load(); n < 3 || n > 8 {
		t.Errorf("task ran %d times in 200ms at a 30ms interval, want 3-8", n)
	}
}

// TestNothingFiresImmediatelyOnStart matters because a deploy restarts the
// process, and every daily job going off on every deploy is its own outage.
func TestNothingFiresImmediatelyOnStart(t *testing.T) {
	s := New(quiet())

	var runs atomic.Int32
	s.Every(time.Hour, "hourly", func(context.Context) error {
		runs.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 10*time.Millisecond)

	if n := runs.Load(); n != 0 {
		t.Errorf("an hourly task ran %d times within 60ms of starting", n)
	}
}

// TestOverlapIsPreventedByDefault is what stops a task slower than its interval
// piling up copies of itself until the process falls over.
func TestOverlapIsPreventedByDefault(t *testing.T) {
	s := New(quiet())

	var concurrent, peak atomic.Int32
	s.Every(10*time.Millisecond, "slow", func(context.Context) error {
		n := concurrent.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		concurrent.Add(-1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 5*time.Millisecond)

	if got := peak.Load(); got > 1 {
		t.Errorf("%d copies of the task ran at once, want 1", got)
	}
}

func TestOverlapCanBeAllowed(t *testing.T) {
	s := New(quiet())

	var concurrent, peak atomic.Int32
	task := s.Every(10*time.Millisecond, "parallel", func(context.Context) error {
		n := concurrent.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		concurrent.Add(-1)
		return nil
	})
	task.AllowOverlap = true

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 5*time.Millisecond)

	if got := peak.Load(); got < 2 {
		t.Errorf("peak concurrency = %d, want more than 1 when overlap is allowed", got)
	}
}

// TestPanicDoesNotStopTheScheduler: one bad task must not take every other task
// down with it.
func TestPanicDoesNotStopTheScheduler(t *testing.T) {
	s := New(quiet())

	var good atomic.Int32
	s.Every(20*time.Millisecond, "explodes", func(context.Context) error {
		panic("task exploded")
	})
	s.Every(20*time.Millisecond, "fine", func(context.Context) error {
		good.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 10*time.Millisecond)

	if good.Load() == 0 {
		t.Error("the healthy task never ran alongside a panicking one")
	}
}

func TestFailingTaskKeepsRunning(t *testing.T) {
	s := New(quiet())

	var runs atomic.Int32
	s.Every(20*time.Millisecond, "flaky", func(context.Context) error {
		runs.Add(1)
		return errors.New("nope")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 10*time.Millisecond)

	// A task that failed once is still scheduled; the next window is its retry.
	if n := runs.Load(); n < 2 {
		t.Errorf("a failing task ran %d times, want it to keep being scheduled", n)
	}
}

func TestRunOnceRunsDueTasksAndReturns(t *testing.T) {
	s := New(quiet())

	var runs atomic.Int32
	s.Every(time.Hour, "hourly", func(context.Context) error {
		runs.Add(1)
		return nil
	})

	// RunOnce is what a system cron calls; it must not care that the interval
	// has not elapsed within this process.
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("RunOnce ran the task %d times, want 1", n)
	}
}

func TestDailyIsDueAfterItsTime(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		at   string
		last time.Time
		want bool
	}{
		// Never run today, and the time has passed.
		{"09:00", time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC), true},
		// Already run since today's target.
		{"09:00", time.Date(2026, 8, 19, 9, 0, 1, 0, time.UTC), false},
		// Today's target has not arrived yet.
		{"10:00", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC), false},
	}

	for _, tc := range tests {
		task := &Task{Name: "daily", Every: 24 * time.Hour, At: tc.at, last: tc.last}
		if got := task.due(now); got != tc.want {
			t.Errorf("at=%s last=%s due = %v, want %v", tc.at, tc.last.Format(time.RFC3339), got, tc.want)
		}
	}
}

func TestInvalidAtNeverFires(t *testing.T) {
	// A malformed time silently running every tick would be far worse than it
	// never running at all.
	task := &Task{Name: "bad", Every: 24 * time.Hour, At: "25:99"}
	if task.due(time.Now()) {
		t.Error("a task with an unparsable At was due")
	}
}

func TestAddRejectsBadTasks(t *testing.T) {
	cases := map[string]*Task{
		"no name":     {Every: time.Minute, Run: func(context.Context) error { return nil }},
		"no run":      {Name: "x", Every: time.Minute},
		"no interval": {Name: "x", Run: func(context.Context) error { return nil }},
	}

	for name, task := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("an invalid task was accepted")
				}
			}()
			New(quiet()).Add(task)
		})
	}
}

func TestDuplicateNamePanics(t *testing.T) {
	s := New(quiet())
	s.Every(time.Minute, "same", func(context.Context) error { return nil })

	defer func() {
		if recover() == nil {
			t.Error("two tasks with the same name were accepted")
		}
	}()
	s.Every(time.Minute, "same", func(context.Context) error { return nil })
}

func TestShutdownWaitsForRunningTasks(t *testing.T) {
	s := New(quiet())

	var finished sync.WaitGroup
	finished.Add(1)
	var completed atomic.Bool

	s.Every(10*time.Millisecond, "slow", func(context.Context) error {
		defer finished.Done()
		time.Sleep(80 * time.Millisecond)
		completed.Store(true)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	time.Sleep(40 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	// A task already started finishes rather than being cut off halfway.
	if !completed.Load() {
		t.Error("shutdown returned before the running task finished")
	}
}
