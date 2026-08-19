// Package ratelimit throttles requests by key.
//
// It exists mainly for authentication endpoints. Rate limiting does not close
// an enumeration leak, but it makes harvesting a user list impractical, and it
// is the only thing standing between a login form and an unlimited password
// guessing loop.
//
// The limiter is in-memory and per-process. That is the right default for a
// single binary; behind several instances each keeps its own count, so the
// effective limit multiplies by the number of instances. Move the store to
// Redis when that stops being acceptable.
package ratelimit

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limiter allows a burst of requests per key, refilling over time.
//
// A token bucket rather than a fixed window: a fixed window lets twice the
// limit through across a window boundary, and resets everyone's quota at the
// same instant, which invites a thundering herd on the second.
type Limiter struct {
	// Limit is how many requests are allowed per Window.
	limit int
	// window is the period Limit applies to.
	window time.Duration
	// burst is the most that can be spent at once.
	burst int

	mu      sync.Mutex
	buckets map[string]*bucket
	// lastSweep is when stale buckets were last discarded, so an unbounded
	// stream of distinct keys cannot grow the map forever.
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

// New builds a limiter allowing limit requests per window, with a burst equal
// to the limit.
func New(limit int, window time.Duration) *Limiter {
	return NewWithBurst(limit, window, limit)
}

// NewWithBurst builds a limiter with an explicit burst size.
func NewWithBurst(limit int, window time.Duration, burst int) *Limiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		limit:   limit,
		window:  window,
		burst:   burst,
		buckets: make(map[string]*bucket),
	}
}

// Allow consumes one token for key. It returns whether the request may proceed
// and, when it may not, how long until the next token is available.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()
	rate := float64(l.limit) / l.window.Seconds()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.burst), seen: now}
		l.buckets[key] = b
	} else {
		b.tokens = math.Min(float64(l.burst), b.tokens+now.Sub(b.seen).Seconds()*rate)
		b.seen = now
	}

	if b.tokens < 1 {
		// Time until the bucket holds one whole token.
		wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
		return false, wait
	}

	b.tokens--
	return true, 0
}

// Reset clears a key's history.
//
// Call it after a successful login: someone who typed their password wrong
// twice and then got it right should not stay near the limit.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Len reports how many keys are being tracked. Tests and diagnostics only.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// sweep discards buckets that have refilled completely, since they are
// indistinguishable from a key that has never been seen. Callers hold the lock.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now

	for key, b := range l.buckets {
		if now.Sub(b.seen) > l.window*2 {
			delete(l.buckets, key)
		}
	}
}

// Config tunes the middleware.
type Config struct {
	// Limiter does the counting.
	Limiter *Limiter
	// KeyFunc identifies the caller. Defaults to the client IP.
	KeyFunc func(*http.Request) string
	// OnLimited renders the rejection. The default is a plain 429.
	OnLimited http.Handler
}

// Middleware rejects requests once a key exceeds its limit.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.Limiter == nil {
		panic("ratelimit: Config.Limiter is required")
	}

	key := cfg.KeyFunc
	if key == nil {
		key = func(r *http.Request) string { return ClientIP(r, false) }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retryAfter := cfg.Limiter.Allow(key(r))
			if ok {
				next.ServeHTTP(w, r)
				return
			}

			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))

			if cfg.OnLimited != nil {
				cfg.OnLimited.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Too many requests. Try again shortly.", http.StatusTooManyRequests)
		})
	}
}

// ClientIP returns the address to rate limit by.
//
// trustProxy must stay false unless the application genuinely sits behind a
// proxy that overwrites X-Forwarded-For. Trusting the header when it is
// reachable directly hands every client a way to spend someone else's quota, or
// to make their own limit unlimited by sending a new value each request — which
// turns rate limiting into decoration.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The list is client, proxy1, proxy2...; the first entry is the
			// original caller.
			first, _, _ := strings.Cut(fwd, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); real != "" {
			return real
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
