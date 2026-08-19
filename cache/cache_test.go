package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// stores returns every driver, so the behaviour tests run against all of them.
func stores(t *testing.T) map[string]Store {
	t.Helper()

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Record{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return map[string]Store{
		"memory": NewMemory(),
		"db":     NewDB(db),
	}
}

func TestSetGetDelete(t *testing.T) {
	ctx := context.Background()

	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := s.Get(ctx, "absent"); ok || err != nil {
				t.Errorf("Get on a missing key = %v, %v", ok, err)
			}

			if err := s.Set(ctx, "k", []byte("v"), Forever); err != nil {
				t.Fatalf("Set: %v", err)
			}

			got, ok, err := s.Get(ctx, "k")
			if err != nil || !ok {
				t.Fatalf("Get = %v, %v", ok, err)
			}
			if string(got) != "v" {
				t.Errorf("value = %q, want v", got)
			}

			if err := s.Delete(ctx, "k"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, ok, _ := s.Get(ctx, "k"); ok {
				t.Error("the key survived Delete")
			}

			// Deleting something that is not there is not an error.
			if err := s.Delete(ctx, "absent"); err != nil {
				t.Errorf("Delete on a missing key: %v", err)
			}
		})
	}
}

func TestExpiry(t *testing.T) {
	ctx := context.Background()

	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if err := s.Set(ctx, "k", []byte("v"), 40*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := s.Get(ctx, "k"); !ok {
				t.Fatal("the value was missing before it expired")
			}

			time.Sleep(70 * time.Millisecond)

			if _, ok, _ := s.Get(ctx, "k"); ok {
				t.Error("an expired value was returned")
			}
		})
	}
}

func TestOverwrite(t *testing.T) {
	ctx := context.Background()

	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			// Writing a key that already exists is the normal case for a cache,
			// not a conflict.
			if err := s.Set(ctx, "k", []byte("first"), Forever); err != nil {
				t.Fatal(err)
			}
			if err := s.Set(ctx, "k", []byte("second"), Forever); err != nil {
				t.Fatalf("overwriting a key failed: %v", err)
			}

			got, _, _ := s.Get(ctx, "k")
			if string(got) != "second" {
				t.Errorf("value = %q, want second", got)
			}
		})
	}
}

func TestClear(t *testing.T) {
	ctx := context.Background()

	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 3; i++ {
				if err := s.Set(ctx, fmt.Sprintf("k%d", i), []byte("v"), Forever); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Clear(ctx); err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if _, ok, _ := s.Get(ctx, "k0"); ok {
				t.Error("a value survived Clear")
			}
		})
	}
}

type profile struct {
	Name  string `json:"name"`
	Views int    `json:"views"`
}

func TestJSONRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()

	want := profile{Name: "Alice", Views: 42}
	if err := SetJSON(ctx, s, "p", want, Forever); err != nil {
		t.Fatal(err)
	}

	var got profile
	ok, err := GetJSON(ctx, s, "p", &got)
	if err != nil || !ok {
		t.Fatalf("GetJSON = %v, %v", ok, err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestUndecodableValueIsDropped covers the struct that changed shape since it
// was cached. Turning that into a permanent error would mean a deploy poisoning
// every cached key until someone flushed it by hand.
func TestUndecodableValueIsDropped(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()

	if err := s.Set(ctx, "p", []byte(`"a string, not an object"`), Forever); err != nil {
		t.Fatal(err)
	}

	var got profile
	ok, err := GetJSON(ctx, s, "p", &got)
	if err != nil {
		t.Errorf("GetJSON returned an error rather than a miss: %v", err)
	}
	if ok {
		t.Error("an undecodable value was reported as a hit")
	}
	if _, present, _ := s.Get(ctx, "p"); present {
		t.Error("the undecodable value was left in the cache")
	}
}

func TestRememberComputesOnceThenCaches(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()

	var calls atomic.Int32
	compute := func(context.Context) (profile, error) {
		calls.Add(1)
		return profile{Name: "Alice", Views: 1}, nil
	}

	for i := 0; i < 3; i++ {
		got, err := Remember(ctx, s, "p", time.Minute, compute)
		if err != nil {
			t.Fatalf("Remember: %v", err)
		}
		if got.Name != "Alice" {
			t.Errorf("got %+v", got)
		}
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("compute ran %d times, want 1", n)
	}
}

// TestRememberCollapsesConcurrentMisses is the stampede case: a popular key
// expiring must not mean every in-flight request recomputes it at once, which
// is how a cache expiring takes the database down.
func TestRememberCollapsesConcurrentMisses(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()

	var calls atomic.Int32
	compute := func(context.Context) (profile, error) {
		calls.Add(1)
		// Long enough that the others pile up behind it.
		time.Sleep(50 * time.Millisecond)
		return profile{Name: "Alice"}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Remember(ctx, s, "stampede", time.Minute, compute); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("compute ran %d times for 20 concurrent misses, want 1", n)
	}
}

func TestRememberPropagatesComputeErrors(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	boom := errors.New("database down")

	_, err := Remember(ctx, s, "k", time.Minute, func(context.Context) (profile, error) {
		return profile{}, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("Remember = %v, want the compute error", err)
	}

	// A failure must not be cached, or one blip poisons the key for its whole
	// TTL.
	if _, ok, _ := s.Get(ctx, "k"); ok {
		t.Error("a failed computation was cached")
	}
}

// TestRememberSurvivesABrokenStore is the promise the package makes: a cache is
// an optimisation, so a broken one makes an application slow, not broken.
func TestRememberSurvivesABrokenStore(t *testing.T) {
	ctx := context.Background()

	got, err := Remember(ctx, brokenStore{}, "k", time.Minute, func(context.Context) (profile, error) {
		return profile{Name: "computed anyway"}, nil
	})
	if err != nil {
		t.Fatalf("Remember with a broken store: %v", err)
	}
	if got.Name != "computed anyway" {
		t.Errorf("got %+v", got)
	}
}

type brokenStore struct{}

func (brokenStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("cache is down")
}
func (brokenStore) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("cache is down")
}
func (brokenStore) Delete(context.Context, string) error { return errors.New("cache is down") }
func (brokenStore) Clear(context.Context) error          { return errors.New("cache is down") }

// TestMemoryEvictsWhenFull guards against the cache becoming a memory leak with
// good intentions: keys derived from user input arrive without limit.
func TestMemoryEvictsWhenFull(t *testing.T) {
	ctx := context.Background()
	m := &Memory{MaxEntries: 10, entries: map[string]entry{}}

	for i := 0; i < 100; i++ {
		if err := m.Set(ctx, fmt.Sprintf("k%d", i), []byte("v"), Forever); err != nil {
			t.Fatal(err)
		}
	}

	if got := m.Len(); got > 10 {
		t.Errorf("holding %d entries, want at most 10", got)
	}
}

func TestMemoryEvictsExpiredFirst(t *testing.T) {
	ctx := context.Background()
	m := &Memory{MaxEntries: 3, entries: map[string]entry{}}

	// Two that are already dead, then fill past the limit.
	_ = m.Set(ctx, "dead1", []byte("v"), time.Nanosecond)
	_ = m.Set(ctx, "dead2", []byte("v"), time.Nanosecond)
	time.Sleep(5 * time.Millisecond)

	_ = m.Set(ctx, "live1", []byte("v"), time.Minute)
	_ = m.Set(ctx, "live2", []byte("v"), time.Minute)
	_ = m.Set(ctx, "live3", []byte("v"), time.Minute)

	// Discarding an expired entry costs nothing, so those go before anything
	// still useful.
	for _, key := range []string{"live1", "live2", "live3"} {
		if _, ok, _ := m.Get(ctx, key); !ok {
			t.Errorf("%s was evicted while expired entries were still held", key)
		}
	}
}

func TestMemoryReturnsACopy(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	_ = m.Set(ctx, "k", []byte("original"), Forever)

	got, _, _ := m.Get(ctx, "k")
	got[0] = 'X'

	// A caller mutating what it was handed must not corrupt what the next
	// reader sees.
	again, _, _ := m.Get(ctx, "k")
	if string(again) != "original" {
		t.Errorf("the stored value was mutated by a caller: %q", again)
	}
}

func TestJitterStaysWithinBounds(t *testing.T) {
	base := time.Minute

	for i := 0; i < 100; i++ {
		got := Jitter(base, 0.2)
		if got < base || got > base+12*time.Second {
			t.Fatalf("Jitter = %v, want between 60s and 72s", got)
		}
	}

	if got := Jitter(Forever, 0.2); got != Forever {
		t.Errorf("Jitter on Forever = %v, want Forever", got)
	}
}

func TestDBPrune(t *testing.T) {
	ctx := context.Background()
	s := stores(t)["db"].(*DB)

	_ = s.Set(ctx, "gone", []byte("v"), time.Nanosecond)
	_ = s.Set(ctx, "stays", []byte("v"), time.Hour)
	time.Sleep(5 * time.Millisecond)

	n, err := s.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d rows, want 1", n)
	}
	if _, ok, _ := s.Get(ctx, "stays"); !ok {
		t.Error("Prune removed a live entry")
	}
}
