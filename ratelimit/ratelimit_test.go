package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestAllowsUpToTheLimit(t *testing.T) {
	l := New(5, time.Minute)

	for i := 1; i <= 5; i++ {
		if ok, _ := l.Allow("key"); !ok {
			t.Fatalf("request %d was rejected, want the first 5 allowed", i)
		}
	}
	if ok, _ := l.Allow("key"); ok {
		t.Error("the 6th request was allowed")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := New(2, time.Minute)

	l.Allow("alice")
	l.Allow("alice")

	// One caller exhausting their quota must not lock anybody else out.
	if ok, _ := l.Allow("bob"); !ok {
		t.Error("bob was rejected because alice hit her limit")
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	// 10 per second, so a token returns every 100ms.
	l := New(10, time.Second)

	for i := 0; i < 10; i++ {
		l.Allow("key")
	}
	if ok, _ := l.Allow("key"); ok {
		t.Fatal("the limit was not enforced")
	}

	time.Sleep(150 * time.Millisecond)

	if ok, _ := l.Allow("key"); !ok {
		t.Error("no token had refilled after 150ms at 10/second")
	}
}

func TestRetryAfterIsReported(t *testing.T) {
	l := New(1, time.Second)
	l.Allow("key")

	ok, retryAfter := l.Allow("key")
	if ok {
		t.Fatal("the second request was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Second {
		t.Errorf("retryAfter = %v, want something under a second", retryAfter)
	}
}

// TestResetClearsHistory is what a successful login calls: someone who mistyped
// their password twice and then got it right should not stay near the limit.
func TestResetClearsHistory(t *testing.T) {
	l := New(2, time.Minute)

	l.Allow("key")
	l.Allow("key")
	if ok, _ := l.Allow("key"); ok {
		t.Fatal("the limit was not enforced")
	}

	l.Reset("key")

	if ok, _ := l.Allow("key"); !ok {
		t.Error("Reset did not clear the key")
	}
}

func TestMiddlewareReturns429WithRetryAfter(t *testing.T) {
	l := New(1, time.Minute)
	h := Middleware(Config{Limiter: l})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/login", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/login", nil))

	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", second.Code)
	}
	// Without Retry-After a client has no way to know when to come back, and
	// well-behaved ones end up hammering.
	retry := second.Header().Get("Retry-After")
	if n, err := strconv.Atoi(retry); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", retry)
	}
}

func TestMiddlewareKeysByIPByDefault(t *testing.T) {
	h := Middleware(Config{Limiter: New(1, time.Minute)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	send := func(addr string) int {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	if got := send("10.0.0.1:1234"); got != http.StatusOK {
		t.Fatalf("first request from .1: %d", got)
	}
	if got := send("10.0.0.1:5678"); got != http.StatusTooManyRequests {
		t.Errorf("second request from the same IP: %d, want 429", got)
	}
	// A different address has its own quota.
	if got := send("10.0.0.2:1234"); got != http.StatusOK {
		t.Errorf("first request from .2: %d, want 200", got)
	}
}

// TestSpoofedForwardedHeaderIsIgnoredByDefault is the reason trustProxy exists
// and defaults to false. If the header were trusted on a directly reachable
// server, a client could send a fresh value every request and never be limited
// at all.
func TestSpoofedForwardedHeaderIsIgnoredByDefault(t *testing.T) {
	h := Middleware(Config{Limiter: New(1, time.Minute)})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	send := func(forwarded string) int {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	send("1.1.1.1")
	if got := send("2.2.2.2"); got != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429: changing X-Forwarded-For bypassed the limit", got)
	}
}

func TestClientIPTrustsProxyWhenAsked(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.9")

	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Errorf("ClientIP(trustProxy=false) = %q, want the socket address", got)
	}
	if got := ClientIP(r, true); got != "203.0.113.7" {
		t.Errorf("ClientIP(trustProxy=true) = %q, want the first forwarded address", got)
	}
}

func TestCustomKeyFunc(t *testing.T) {
	// Limiting by email as well as IP is what stops one address being attacked
	// from many machines.
	h := Middleware(Config{
		Limiter: New(1, time.Minute),
		KeyFunc: func(r *http.Request) string { return r.URL.Query().Get("email") },
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	send := func(email string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/login?email="+email, nil))
		return rec.Code
	}

	send("a@example.com")
	if got := send("a@example.com"); got != http.StatusTooManyRequests {
		t.Errorf("same email twice = %d, want 429", got)
	}
	if got := send("b@example.com"); got != http.StatusOK {
		t.Errorf("different email = %d, want 200", got)
	}
}

func TestStaleKeysAreDiscarded(t *testing.T) {
	l := New(5, 20*time.Millisecond)

	for i := 0; i < 100; i++ {
		l.Allow("key-" + strconv.Itoa(i))
	}
	if l.Len() < 100 {
		t.Fatalf("tracking %d keys, want 100", l.Len())
	}

	// Past two windows the buckets have fully refilled and carry no
	// information, so an endless stream of distinct keys must not grow the map
	// without bound.
	time.Sleep(60 * time.Millisecond)
	l.Allow("trigger-sweep")

	if l.Len() > 10 {
		t.Errorf("still tracking %d keys after the sweep", l.Len())
	}
}
