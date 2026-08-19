package mongo

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	rylamongo "github.com/Dshonored/ryla/mongo"
	"github.com/Dshonored/ryla/queue"
)

// --- unit tests, no server needed -----------------------------------------

// TestTimeoutFallsBackToTheSharedReservationTimeout protects a driver built as
// a struct literal rather than through New. A zero Timeout must not mean zero:
// every reservation would be stale the instant it was made, and a job already
// running would be handed straight to a second worker.
func TestTimeoutFallsBackToTheSharedReservationTimeout(t *testing.T) {
	var d Driver
	if got := d.timeout(); got != queue.ReservationTimeout {
		t.Errorf("timeout() = %v, want %v", got, queue.ReservationTimeout)
	}

	d.Timeout = time.Minute
	if got := d.timeout(); got != time.Minute {
		t.Errorf("an explicit timeout was ignored: %v", got)
	}

	if got := New(nil).timeout(); got != queue.ReservationTimeout {
		t.Errorf("New left the timeout at %v", got)
	}
}

// TestClaimIndexIsDeclared protects the read path. Claim runs on every poll of
// every worker, so it has to be index-backed; the declaration in init is the
// only thing standing between that and a collection scan.
func TestClaimIndexIsDeclared(t *testing.T) {
	var found bool
	for _, idx := range rylamongo.Registered() {
		if idx.Collection != JobsCollection || idx.Name != "jobs_claim" {
			continue
		}
		found = true

		// Equality first, then the sort and range field: a compound index is
		// only usable from a prefix, so the order is the index.
		if len(idx.Keys) < 2 || idx.Keys[0].Key != "queue" || idx.Keys[1].Key != "available_at" {
			t.Errorf("claim index keys = %v", idx.Keys)
		}
	}
	if !found {
		t.Error("no claim index was registered for the jobs collection")
	}
}

// TestStoredJobSurvivesTheTripThroughQueueRecord covers the id bridge: the
// worker only ever holds a queue.Record, and Complete, Release and Fail have to
// find the document again from it.
func TestStoredJobSurvivesTheTripThroughQueueRecord(t *testing.T) {
	reserved := time.Now().UTC().Truncate(time.Millisecond)

	doc := jobDoc{
		ID:          42,
		Queue:       "mail",
		Name:        "send.welcome",
		Payload:     `{"user":7}`,
		Attempts:    2,
		MaxAttempts: 3,
		AvailableAt: reserved,
		ReservedAt:  &reserved,
		LastError:   "smtp down",
		CreatedAt:   reserved,
	}

	rec := doc.record()
	if rec.ID != 42 || int64(rec.ID) != doc.ID {
		t.Errorf("id = %d, want 42", rec.ID)
	}
	if rec.Queue != "mail" || rec.Name != "send.welcome" || rec.Payload != `{"user":7}` {
		t.Errorf("record = %+v", rec)
	}
	if rec.Attempts != 2 || rec.MaxAttempts != 3 {
		t.Errorf("attempts = %d/%d", rec.Attempts, rec.MaxAttempts)
	}
	if rec.ReservedAt == nil || !rec.ReservedAt.Equal(reserved) {
		t.Errorf("reserved_at = %v", rec.ReservedAt)
	}
	if rec.LastError != "smtp down" {
		t.Errorf("last error = %q", rec.LastError)
	}
}

// TestTruncateBoundsTheStoredError keeps a runaway error message — a stack
// trace, a whole HTTP response body — from being written back onto the job.
func TestTruncateBoundsTheStoredError(t *testing.T) {
	if got := truncate("short", 4000); got != "short" {
		t.Errorf("a short message was altered: %q", got)
	}
	if got := truncate(strings.Repeat("x", 5000), 4000); len(got) != 4000 {
		t.Errorf("length = %d, want 4000", len(got))
	}
}

// --- integration tests ------------------------------------------------------

// live connects to a real MongoDB, or skips.
//
// There is no in-process MongoDB the way miniredis stands in for Redis, so
// these run where a server exists — CI provides one — and are skipped elsewhere
// rather than silently not testing anything.
//
// The database is this package's own: sibling packages drop theirs between
// tests, and a shared name would let one suite pull the collections out from
// under another.
func live(t *testing.T) *Driver {
	t.Helper()

	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("skipping: set MONGO_TEST_URI to run the MongoDB integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, db, err := rylamongo.Open(ctx, rylamongo.Config{URI: uri, Database: "ryla_queue_test"})
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	// A fresh database per test, so one leaving jobs behind cannot change what
	// the next one claims.
	if err := db.Drop(ctx); err != nil {
		t.Fatalf("drop: %v", err)
	}
	return New(db)
}

// push queues one job due now and returns it.
func push(t *testing.T, d *Driver, q, name string) *queue.Record {
	t.Helper()

	rec := &queue.Record{
		Queue:       q,
		Name:        name,
		Payload:     `{}`,
		MaxAttempts: 3,
		AvailableAt: time.Now(),
		CreatedAt:   time.Now(),
	}
	if err := d.Push(context.Background(), rec); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if rec.ID == 0 {
		t.Fatal("Push left the record without an id")
	}
	return rec
}

// TestLiveClaimIsExclusiveUnderContention is the property the whole package
// exists to hold: however many workers poll at once, a job runs once. A queue
// that hands the same job to two workers sends the email twice, charges the
// card twice, and is worse than no queue at all.
func TestLiveClaimIsExclusiveUnderContention(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	pushed := push(t, d, "contended", "job.once")

	const workers = 16

	var (
		start   = make(chan struct{})
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []*queue.Record
		fails   []error
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			rec, err := d.Claim(ctx, "contended")

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			if rec != nil {
				winners = append(winners, rec)
			}
		}()
	}

	close(start)
	wg.Wait()

	for _, err := range fails {
		t.Errorf("Claim: %v", err)
	}
	if len(winners) != 1 {
		t.Fatalf("%d workers claimed the job, want exactly 1", len(winners))
	}
	if winners[0].ID != pushed.ID {
		t.Errorf("claimed id %d, pushed %d", winners[0].ID, pushed.ID)
	}
	// The attempt count is what a job reads to decide it has been retried
	// enough, so the losers must not have inflated it either.
	if winners[0].Attempts != 1 {
		t.Errorf("attempts = %d after one claim, want 1", winners[0].Attempts)
	}
}

// TestLiveClaimTakesTheOldestDueJobAndNothingElse covers ordering and the
// availability gate together: a job scheduled for later must stay invisible,
// or a delayed retry runs immediately and defeats the backoff.
func TestLiveClaimTakesTheOldestDueJobAndNothingElse(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	first := push(t, d, queue.Default, "job.first")
	second := push(t, d, queue.Default, "job.second")

	later := &queue.Record{
		Queue:       queue.Default,
		Name:        "job.later",
		MaxAttempts: 3,
		AvailableAt: time.Now().Add(time.Hour),
		CreatedAt:   time.Now(),
	}
	if err := d.Push(ctx, later); err != nil {
		t.Fatalf("Push: %v", err)
	}

	for _, want := range []*queue.Record{first, second} {
		got, err := d.Claim(ctx, queue.Default)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if got == nil {
			t.Fatalf("nothing claimed, expected %s", want.Name)
		}
		if got.ID != want.ID {
			t.Errorf("claimed %d (%s), want %d (%s)", got.ID, got.Name, want.ID, want.Name)
		}
	}

	got, err := d.Claim(ctx, queue.Default)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != nil {
		t.Errorf("claimed %s, which is not due until %v", got.Name, got.AvailableAt)
	}
}

// TestLiveClaimIgnoresOtherQueues protects the reason queues have names: slow
// work on one queue must not be picked up by a worker draining another.
func TestLiveClaimIgnoresOtherQueues(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, "reports", "job.slow")

	got, err := d.Claim(ctx, queue.Default)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got != nil {
		t.Errorf("a worker on %q claimed %q from another queue", queue.Default, got.Name)
	}
}

// TestLiveAbandonedReservationIsReclaimed covers the killed worker: without a
// reservation timeout its job stays reserved forever and silently never runs.
func TestLiveAbandonedReservationIsReclaimed(t *testing.T) {
	d := live(t)
	d.Timeout = 100 * time.Millisecond
	ctx := context.Background()

	pushed := push(t, d, queue.Default, "job.abandoned")

	first, err := d.Claim(ctx, queue.Default)
	if err != nil || first == nil {
		t.Fatalf("first Claim = %v, %v", first, err)
	}

	// Still held: another worker polling now must come away with nothing.
	if held, err := d.Claim(ctx, queue.Default); err != nil || held != nil {
		t.Fatalf("a reserved job was handed out again: %v, %v", held, err)
	}

	time.Sleep(200 * time.Millisecond)

	again, err := d.Claim(ctx, queue.Default)
	if err != nil {
		t.Fatalf("Claim after the reservation lapsed: %v", err)
	}
	if again == nil {
		t.Fatal("an abandoned job was never reclaimed")
	}
	if again.ID != pushed.ID {
		t.Errorf("reclaimed %d, want %d", again.ID, pushed.ID)
	}
	// Attempts counts tries, including the one the dead worker never finished,
	// so the job still gives up rather than looping forever.
	if again.Attempts != 2 {
		t.Errorf("attempts = %d after a reclaim, want 2", again.Attempts)
	}
}

// TestLiveReleaseReschedulesWithItsReason covers the retry path: the job comes
// back, but not before its backoff, and it carries why it failed.
func TestLiveReleaseReschedulesWithItsReason(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, queue.Default, "job.flaky")

	rec, err := d.Claim(ctx, queue.Default)
	if err != nil || rec == nil {
		t.Fatalf("Claim = %v, %v", rec, err)
	}

	if err := d.Release(ctx, rec, time.Now().Add(time.Hour), "smtp refused"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if got, err := d.Claim(ctx, queue.Default); err != nil || got != nil {
		t.Fatalf("a released job was claimed before it was due: %v, %v", got, err)
	}

	// Due again: released, not reserved.
	if err := d.Release(ctx, rec, time.Now().Add(-time.Second), "smtp refused"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	got, err := d.Claim(ctx, queue.Default)
	if err != nil || got == nil {
		t.Fatalf("Claim after release = %v, %v", got, err)
	}
	if got.ID != rec.ID {
		t.Errorf("claimed %d, want the released job %d", got.ID, rec.ID)
	}
	if got.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", got.Attempts)
	}
	if got.LastError != "smtp refused" {
		t.Errorf("last error = %q", got.LastError)
	}
}

// TestLiveCompleteRemovesTheJob checks a finished job cannot be claimed again.
func TestLiveCompleteRemovesTheJob(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, queue.Default, "job.done")

	rec, err := d.Claim(ctx, queue.Default)
	if err != nil || rec == nil {
		t.Fatalf("Claim = %v, %v", rec, err)
	}
	if err := d.Complete(ctx, rec); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	n, err := d.Pending(ctx, queue.Default)
	if err != nil || n != 0 {
		t.Errorf("Pending = %d, %v, want 0", n, err)
	}
}

// TestLiveFailKeepsTheJobAndRetryPutsItBack protects the thing that turns a
// dead job into an incident someone can fix: a job that gave up is kept, is
// listed with its reason, and can be re-queued.
func TestLiveFailKeepsTheJobAndRetryPutsItBack(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, "mail", "job.doomed")

	rec, err := d.Claim(ctx, "mail")
	if err != nil || rec == nil {
		t.Fatalf("Claim = %v, %v", rec, err)
	}
	if err := d.Fail(ctx, rec, "mailbox does not exist"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	if n, err := d.Pending(ctx, "mail"); err != nil || n != 0 {
		t.Errorf("Pending = %d, %v, want 0", n, err)
	}

	failed, err := d.Failed(ctx, 10)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("%d failed jobs, want 1", len(failed))
	}
	if failed[0].ID != rec.ID || failed[0].Name != "job.doomed" {
		t.Errorf("failed record = %+v", failed[0])
	}
	if failed[0].LastError != "mailbox does not exist" {
		t.Errorf("last error = %q", failed[0].LastError)
	}
	if failed[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1", failed[0].Attempts)
	}

	if err := d.Retry(ctx, failed[0].ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	if n, err := d.Pending(ctx, "mail"); err != nil || n != 1 {
		t.Errorf("Pending after Retry = %d, %v, want 1", n, err)
	}
	if left, err := d.Failed(ctx, 10); err != nil || len(left) != 0 {
		t.Errorf("%d failed jobs after Retry, want 0 (%v)", len(left), err)
	}

	// Back on its own queue, due now, with a clean slate of attempts.
	back, err := d.Claim(ctx, "mail")
	if err != nil || back == nil {
		t.Fatalf("Claim after Retry = %v, %v", back, err)
	}
	if back.Name != "job.doomed" {
		t.Errorf("claimed %q", back.Name)
	}
	if back.Attempts != 1 {
		t.Errorf("attempts = %d on a retried job, want 1", back.Attempts)
	}
}

// TestLiveFailIsSafeToRepeat covers the crash between the two writes: recording
// the same failure twice must replace the record rather than collide on its id
// and leave the job stuck.
func TestLiveFailIsSafeToRepeat(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, queue.Default, "job.doomed")

	rec, err := d.Claim(ctx, queue.Default)
	if err != nil || rec == nil {
		t.Fatalf("Claim = %v, %v", rec, err)
	}

	if err := d.Fail(ctx, rec, "first"); err != nil {
		t.Fatalf("first Fail: %v", err)
	}
	if err := d.Fail(ctx, rec, "second"); err != nil {
		t.Fatalf("second Fail: %v", err)
	}

	failed, err := d.Failed(ctx, 10)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("%d failed records for one job, want 1", len(failed))
	}
	if failed[0].LastError != "second" {
		t.Errorf("last error = %q, want the newer reason", failed[0].LastError)
	}
}

// TestLiveRetryRejectsAnUnknownID keeps a typo at the command line from being
// reported as a successful re-queue.
func TestLiveRetryRejectsAnUnknownID(t *testing.T) {
	d := live(t)

	if err := d.Retry(context.Background(), 9999); err == nil {
		t.Error("Retry of a job that was never failed was accepted")
	}
}

// TestLivePendingCountsOneQueue protects the number an operator watches to
// decide whether to add workers.
func TestLivePendingCountsOneQueue(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	push(t, d, queue.Default, "job.a")
	push(t, d, queue.Default, "job.b")
	push(t, d, "reports", "job.c")

	if n, err := d.Pending(ctx, queue.Default); err != nil || n != 2 {
		t.Errorf("Pending(%q) = %d, %v, want 2", queue.Default, n, err)
	}
	if n, err := d.Pending(ctx, "reports"); err != nil || n != 1 {
		t.Errorf("Pending(reports) = %d, %v, want 1", n, err)
	}
	if n, err := d.Pending(ctx, "empty"); err != nil || n != 0 {
		t.Errorf("Pending(empty) = %d, %v, want 0", n, err)
	}
}

// TestLiveIDsAreNeverReused protects the id bridge under concurrency: two jobs
// sharing an id would mean one overwriting the other.
func TestLiveIDsAreNeverReused(t *testing.T) {
	d := live(t)
	ctx := context.Background()

	const pushes = 32

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[uint]bool{}
	)

	for i := 0; i < pushes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			rec := &queue.Record{
				Queue:       queue.Default,
				Name:        "job.numbered",
				MaxAttempts: 3,
				AvailableAt: time.Now(),
				CreatedAt:   time.Now(),
			}
			if err := d.Push(ctx, rec); err != nil {
				t.Errorf("Push: %v", err)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if seen[rec.ID] {
				t.Errorf("id %d was minted twice", rec.ID)
			}
			seen[rec.ID] = true
		}()
	}
	wg.Wait()

	if n, err := d.Pending(ctx, queue.Default); err != nil || n != pushes {
		t.Errorf("Pending = %d, %v, want %d", n, err, pushes)
	}
}
