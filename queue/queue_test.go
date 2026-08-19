package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := uniqueDSN(t)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Record{}, &FailedRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func testDriver(t *testing.T) *DBDriver {
	t.Helper()
	reset()
	t.Cleanup(reset)
	return NewDBDriver(testDB(t))
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- test jobs -------------------------------------------------------------

var runs struct {
	sync.Mutex
	greeted []string
}

type greet struct {
	To string `json:"to"`
}

func (greet) Name() string { return "test.greet" }

func (g greet) Run(context.Context) error {
	runs.Lock()
	defer runs.Unlock()
	runs.greeted = append(runs.greeted, g.To)
	return nil
}

var boomAttempts atomic.Int32

type boom struct{}

func (boom) Name() string { return "test.boom" }
func (boom) Run(context.Context) error {
	boomAttempts.Add(1)
	return errors.New("nope")
}
func (boom) MaxAttempts() int          { return 2 }
func (boom) Backoff(int) time.Duration { return 0 }

type panicky struct{}

func (panicky) Name() string              { return "test.panic" }
func (panicky) Run(context.Context) error { panic("job exploded") }
func (panicky) MaxAttempts() int          { return 1 }

type onOwnQueue struct{}

func (onOwnQueue) Name() string              { return "test.mail" }
func (onOwnQueue) Run(context.Context) error { return nil }
func (onOwnQueue) Queue() string             { return "mail" }

// --- tests -----------------------------------------------------------------

func TestDispatchAndRun(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &greet{} })

	runs.Lock()
	runs.greeted = nil
	runs.Unlock()

	ctx := context.Background()
	disp := NewDispatcher(d)
	if err := disp.Dispatch(ctx, &greet{To: "alice"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// The payload has to survive the round trip through the database.
	rec, err := d.Claim(ctx, Default)
	if err != nil || rec == nil {
		t.Fatalf("Claim: %v, %v", rec, err)
	}
	job, err := decode(rec)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := job.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	runs.Lock()
	defer runs.Unlock()
	if len(runs.greeted) != 1 || runs.greeted[0] != "alice" {
		t.Errorf("job ran with %v, want [alice]", runs.greeted)
	}
}

func TestDispatchRejectsUnregisteredJobs(t *testing.T) {
	d := testDriver(t)

	// Better to fail at dispatch than to write a row no worker can ever run,
	// and discover it days later.
	err := NewDispatcher(d).Dispatch(context.Background(), &greet{To: "alice"})
	if !errors.Is(err, ErrUnknownJob) {
		t.Errorf("Dispatch of an unregistered job = %v, want ErrUnknownJob", err)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	reset()
	t.Cleanup(reset)

	defer func() {
		if recover() == nil {
			t.Fatal("registering two jobs with the same name did not panic")
		}
	}()

	Register(func() Job { return &greet{} })
	Register(func() Job { return &greet{} })
}

func TestDelayedJobIsNotClaimedEarly(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &greet{} })

	ctx := context.Background()
	if err := NewDispatcher(d).DispatchIn(ctx, &greet{To: "later"}, time.Hour); err != nil {
		t.Fatal(err)
	}

	rec, err := d.Claim(ctx, Default)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Error("a job scheduled an hour out was claimed immediately")
	}
}

func TestJobsGoToTheirOwnQueue(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &onOwnQueue{} })

	ctx := context.Background()
	if err := NewDispatcher(d).Dispatch(ctx, &onOwnQueue{}); err != nil {
		t.Fatal(err)
	}

	// Slow work on its own queue must not be picked up by the default worker,
	// or it starves everything else.
	rec, err := d.Claim(ctx, Default)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Error("a job on the mail queue was claimed from the default queue")
	}

	rec, err = d.Claim(ctx, "mail")
	if err != nil || rec == nil {
		t.Errorf("Claim(mail) = %v, %v", rec, err)
	}
}

// TestClaimIsExclusive is the property everything else rests on: two workers
// racing for one job must produce one winner, not two runs.
func TestClaimIsExclusive(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &greet{} })

	ctx := context.Background()
	if err := NewDispatcher(d).Dispatch(ctx, &greet{To: "once"}); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var claimed atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec, err := d.Claim(ctx, Default); err == nil && rec != nil {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := claimed.Load(); got != 1 {
		t.Errorf("%d workers claimed the same job, want exactly 1", got)
	}
}

func TestWorkerRetriesThenFails(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &boom{} })
	boomAttempts.Store(0)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := NewDispatcher(d).Dispatch(ctx, &boom{}); err != nil {
		t.Fatal(err)
	}

	w := &Worker{Driver: d, Log: quiet(), PollInterval: 10 * time.Millisecond}
	go w.Run(ctx)

	// MaxAttempts is 2 with no backoff, so it should give up quickly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var failed int64
		d.DB.Model(&FailedRecord{}).Count(&failed)
		if failed == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if got := boomAttempts.Load(); got != 2 {
		t.Errorf("job ran %d times, want 2 (its MaxAttempts)", got)
	}

	var pending, failed int64
	d.DB.Model(&Record{}).Count(&pending)
	d.DB.Model(&FailedRecord{}).Count(&failed)

	if pending != 0 {
		t.Errorf("%d jobs still pending, want 0", pending)
	}
	// A job that gave up is kept, not discarded: it is usually the first sign
	// of a broken dependency.
	if failed != 1 {
		t.Errorf("%d failed jobs recorded, want 1", failed)
	}
}

// TestPanicDoesNotKillTheWorker matters because one bad job would otherwise
// stop every other job in the process with it.
func TestPanicDoesNotKillTheWorker(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &panicky{} })
	Register(func() Job { return &greet{} })

	runs.Lock()
	runs.greeted = nil
	runs.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	disp := NewDispatcher(d)
	if err := disp.Dispatch(ctx, &panicky{}); err != nil {
		t.Fatal(err)
	}
	if err := disp.Dispatch(ctx, &greet{To: "survivor"}); err != nil {
		t.Fatal(err)
	}

	w := &Worker{Driver: d, Log: quiet(), PollInterval: 10 * time.Millisecond}
	go w.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runs.Lock()
		done := len(runs.greeted) > 0
		runs.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	runs.Lock()
	defer runs.Unlock()
	if len(runs.greeted) != 1 {
		t.Error("the job after a panicking one never ran")
	}
}

func TestStaleReservationIsReclaimed(t *testing.T) {
	d := testDriver(t)
	d.Timeout = 50 * time.Millisecond
	Register(func() Job { return &greet{} })

	ctx := context.Background()
	if err := NewDispatcher(d).Dispatch(ctx, &greet{To: "stuck"}); err != nil {
		t.Fatal(err)
	}

	first, err := d.Claim(ctx, Default)
	if err != nil || first == nil {
		t.Fatalf("first Claim: %v, %v", first, err)
	}

	// Simulates a worker killed mid-job: without a reservation timeout the row
	// stays reserved forever and the job silently never completes.
	if rec, _ := d.Claim(ctx, Default); rec != nil {
		t.Fatal("the job was claimed twice while still reserved")
	}

	time.Sleep(80 * time.Millisecond)

	if rec, err := d.Claim(ctx, Default); err != nil || rec == nil {
		t.Error("a job abandoned by a dead worker was never reclaimed")
	}
}

func TestFailedJobCanBeRetried(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &greet{} })

	ctx := context.Background()
	rec := &Record{
		Queue: Default, Name: "test.greet", Payload: `{"to":"alice"}`,
		Attempts: 3, MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := d.Push(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := d.Fail(ctx, rec, "gave up"); err != nil {
		t.Fatal(err)
	}

	failed, err := d.Failed(ctx, 10)
	if err != nil || len(failed) != 1 {
		t.Fatalf("Failed() = %v, %v", failed, err)
	}

	if err := d.Retry(ctx, failed[0].ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Back on the queue, with its attempts reset so it gets a fair run.
	claimed, err := d.Claim(ctx, Default)
	if err != nil || claimed == nil {
		t.Fatalf("Claim after retry: %v, %v", claimed, err)
	}
	if claimed.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after a retry", claimed.Attempts)
	}

	remaining, _ := d.Failed(ctx, 10)
	if len(remaining) != 0 {
		t.Errorf("%d rows left in failed_jobs after a retry", len(remaining))
	}
}

func TestPending(t *testing.T) {
	d := testDriver(t)
	Register(func() Job { return &greet{} })

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := NewDispatcher(d).Dispatch(ctx, &greet{To: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := d.Pending(ctx, Default)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("Pending = %d, want 3", n)
	}
}

func TestDefaultBackoffGrowsAndIsCapped(t *testing.T) {
	if a, b := DefaultBackoff(1), DefaultBackoff(2); b <= a {
		t.Errorf("backoff did not grow: %v then %v", a, b)
	}
	// Retrying a struggling dependency every second only adds load.
	if got := DefaultBackoff(1000); got != 10*time.Minute {
		t.Errorf("DefaultBackoff(1000) = %v, want the 10m ceiling", got)
	}
}

// dbCounter makes every in-memory database unique.
//
// cache=shared means a DSN names one database for the whole process, so reusing
// the test's name reuses its data. That is invisible on a single run and breaks
// immediately under `go test -count=2`, where the second iteration inherits
// whatever the first left behind.
var dbCounter atomic.Int64

func uniqueDSN(t *testing.T) string {
	return fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), dbCounter.Add(1))
}
