package redis_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/Dshonored/ryla/cache"
	cacheredis "github.com/Dshonored/ryla/cache/redis"
	"github.com/Dshonored/ryla/cookie"
	"github.com/Dshonored/ryla/queue"
	queueredis "github.com/Dshonored/ryla/queue/redis"
	"github.com/Dshonored/ryla/session"
	sessionredis "github.com/Dshonored/ryla/session/redis"
)

// server starts an in-process Redis speaking the real protocol, so these tests
// exercise the actual client and the actual Lua script rather than a stub.
func server(t *testing.T) (*miniredis.Miniredis, *goredis.Client) {
	t.Helper()

	s := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	return s, client
}

// --- cache -----------------------------------------------------------------

func TestCacheRoundTrip(t *testing.T) {
	_, client := server(t)
	s := cacheredis.New(client, "app")
	ctx := context.Background()

	if _, ok, err := s.Get(ctx, "absent"); ok || err != nil {
		t.Errorf("Get on a missing key = %v, %v", ok, err)
	}

	if err := s.Set(ctx, "k", []byte("v"), cache.Forever); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, ok, err := s.Get(ctx, "k")
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("Get = %q, %v, %v", got, ok, err)
	}

	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("the key survived Delete")
	}
}

func TestCacheExpiry(t *testing.T) {
	mr, client := server(t)
	s := cacheredis.New(client, "app")
	ctx := context.Background()

	if err := s.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}

	// miniredis has a controllable clock, so this tests expiry without waiting
	// a real minute.
	mr.FastForward(2 * time.Minute)

	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("an expired key was returned")
	}
}

// TestCacheClearOnlyRemovesItsOwnKeys is why Clear does not use FLUSHDB:
// wiping sessions and queued jobs along with the cache would be a very bad
// surprise from a method with this name.
func TestCacheClearOnlyRemovesItsOwnKeys(t *testing.T) {
	_, client := server(t)
	ctx := context.Background()

	s := cacheredis.New(client, "cache")
	if err := s.Set(ctx, "k", []byte("v"), cache.Forever); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "session:abc", "important", 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := s.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("Clear left a cache key behind")
	}
	if err := client.Get(ctx, "session:abc").Err(); err != nil {
		t.Errorf("Clear removed a key belonging to something else: %v", err)
	}
}

func TestCacheRememberWorksOverRedis(t *testing.T) {
	_, client := server(t)
	s := cacheredis.New(client, "app")
	ctx := context.Background()

	var calls atomic.Int32
	compute := func(context.Context) (string, error) {
		calls.Add(1)
		return "computed", nil
	}

	for i := 0; i < 3; i++ {
		got, err := cache.Remember(ctx, s, "k", time.Minute, compute)
		if err != nil || got != "computed" {
			t.Fatalf("Remember = %q, %v", got, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("compute ran %d times, want 1", n)
	}
}

// --- sessions --------------------------------------------------------------

func sessionStore(t *testing.T, client *goredis.Client) *sessionredis.Store {
	t.Helper()
	return sessionredis.New(client, cookie.New("test-key", false), time.Hour, "session")
}

func TestSessionRoundTrip(t *testing.T) {
	_, client := server(t)
	store := sessionStore(t, client)

	rec := httptest.NewRecorder()
	sess := session.New(time.Hour)
	sess.Put("colour", "blue")

	if err := store.Save(rec, httptest.NewRequest(http.MethodGet, "/", nil), sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		next.AddCookie(c)
	}

	loaded, err := store.Load(next)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Get("colour"); got != "blue" {
		t.Errorf("value = %q, want blue", got)
	}
}

// TestSessionCanBeRevoked is the property a cookie store cannot offer, and what
// "log out everywhere" depends on.
func TestSessionCanBeRevoked(t *testing.T) {
	_, client := server(t)
	store := sessionStore(t, client)
	ctx := context.Background()

	var cookies []*http.Cookie
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		sess := session.New(time.Hour)
		sess.Put(session.UserIDKey, "42")

		if err := store.Save(rec, httptest.NewRequest(http.MethodGet, "/", nil), sess); err != nil {
			t.Fatal(err)
		}
		cookies = append(cookies, rec.Result().Cookies()...)
	}

	if err := store.DestroyForUser(ctx, "42"); err != nil {
		t.Fatalf("DestroyForUser: %v", err)
	}

	for _, c := range cookies {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(c)
		if _, err := store.Load(r); err == nil {
			t.Error("a session survived being revoked")
		}
	}
}

func TestSessionMissingIsNotAnError(t *testing.T) {
	_, client := server(t)
	store := sessionStore(t, client)

	if _, err := store.Load(httptest.NewRequest(http.MethodGet, "/", nil)); err != session.ErrNotFound {
		t.Errorf("Load with no cookie = %v, want ErrNotFound", err)
	}
}

// --- queue -----------------------------------------------------------------

func TestQueuePushAndClaim(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: `{"to":"alice"}`,
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := d.Push(ctx, rec); err != nil {
		t.Fatalf("Push: %v", err)
	}

	got, err := d.Claim(ctx, queue.Default)
	if err != nil || got == nil {
		t.Fatalf("Claim = %v, %v", got, err)
	}
	if got.Name != "redis.noop" || got.Payload != `{"to":"alice"}` {
		t.Errorf("claimed %+v", got)
	}
	// The attempt count comes back from the script, not from the caller.
	if got.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", got.Attempts)
	}

	// Reserved, so nothing else may take it.
	if again, _ := d.Claim(ctx, queue.Default); again != nil {
		t.Error("a reserved job was claimed twice")
	}
}

// TestQueueClaimIsExclusive is the property the Lua script exists for: several
// workers racing must produce one winner, not two runs of the same job.
func TestQueueClaimIsExclusive(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := d.Push(ctx, rec); err != nil {
		t.Fatal(err)
	}

	var claimed atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := d.Claim(ctx, queue.Default); err == nil && got != nil {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := claimed.Load(); got != 1 {
		t.Errorf("%d workers claimed the same job, want exactly 1", got)
	}
}

func TestQueueDelayedJobIsNotClaimedEarly(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	if err := d.Push(ctx, rec); err != nil {
		t.Fatal(err)
	}

	if got, _ := d.Claim(ctx, queue.Default); got != nil {
		t.Error("a job scheduled an hour out was claimed immediately")
	}
}

func TestQueueStaleReservationIsReclaimed(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	d.Timeout = 50 * time.Millisecond
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := d.Push(ctx, rec); err != nil {
		t.Fatal(err)
	}

	if got, _ := d.Claim(ctx, queue.Default); got == nil {
		t.Fatal("first claim returned nothing")
	}

	// Simulates a worker killed mid-job. Without a reservation timeout the job
	// would be held forever and silently never complete.
	time.Sleep(80 * time.Millisecond)

	if got, _ := d.Claim(ctx, queue.Default); got == nil {
		t.Error("a job abandoned by a dead worker was never reclaimed")
	}
}

func TestQueueCompleteRemovesTheJob(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	_ = d.Push(ctx, rec)

	claimed, _ := d.Claim(ctx, queue.Default)
	if err := d.Complete(ctx, claimed); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	n, err := d.Pending(ctx, queue.Default)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d jobs still pending after Complete", n)
	}
}

func TestQueueReleaseSchedulesARetry(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	_ = d.Push(ctx, rec)

	claimed, _ := d.Claim(ctx, queue.Default)
	if err := d.Release(ctx, claimed, time.Now().Add(time.Hour), "smtp down"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Back on the queue, but not due yet.
	if n, _ := d.Pending(ctx, queue.Default); n != 1 {
		t.Errorf("pending = %d, want 1", n)
	}
	if got, _ := d.Claim(ctx, queue.Default); got != nil {
		t.Error("a job released for an hour's time was claimed immediately")
	}
}

func TestQueueFailRecordsTheJob(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	rec := &queue.Record{
		Queue: queue.Default, Name: "redis.noop", Payload: "{}",
		MaxAttempts: 1, AvailableAt: time.Now(), CreatedAt: time.Now(),
	}
	_ = d.Push(ctx, rec)

	claimed, _ := d.Claim(ctx, queue.Default)
	if err := d.Fail(ctx, claimed, "gave up"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	failed, err := d.Failed(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Kept rather than discarded: a job that gave up is usually the first sign
	// of a broken dependency.
	if len(failed) != 1 || failed[0].LastError != "gave up" {
		t.Errorf("Failed() = %+v", failed)
	}
	if n, _ := d.Pending(ctx, queue.Default); n != 0 {
		t.Errorf("%d jobs still pending after Fail", n)
	}
}

func TestQueuesAreIndependent(t *testing.T) {
	_, client := server(t)
	d := queueredis.New(client, "queue")
	ctx := context.Background()

	_ = d.Push(ctx, &queue.Record{
		Queue: "mail", Name: "redis.noop", Payload: "{}",
		MaxAttempts: 3, AvailableAt: time.Now(), CreatedAt: time.Now(),
	})

	// Slow work on its own queue must not be picked up by the default worker.
	if got, _ := d.Claim(ctx, queue.Default); got != nil {
		t.Error("a job on the mail queue was claimed from the default queue")
	}
	if got, _ := d.Claim(ctx, "mail"); got == nil {
		t.Error("the job was not claimable from its own queue")
	}
}
