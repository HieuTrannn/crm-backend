package auth

import (
	"context"
	"sync"
	"time"
)

// LoginLimiter throttles failed logins per key (IP + email). It is an interface
// so the in-memory version can be swapped for Redis when running several instances.
type LoginLimiter interface {
	// Allow reports whether another attempt is permitted for key.
	Allow(ctx context.Context, key string) (bool, error)
	// Fail records one failed attempt for key.
	Fail(ctx context.Context, key string) error
}

// MemoryLimiter is a token bucket per key: capacity failures are allowed at
// once, then one more every refillEvery. NFR-SEC-02 (5 per 15 minutes) is
// capacity 5, refillEvery 3 minutes.
// shortcut: state lives in one process; with more than one API instance use a Redis LoginLimiter.
type MemoryLimiter struct {
	capacity    float64
	refillEvery time.Duration
	now         func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewMemoryLimiter creates a limiter allowing capacity failures, refilling one per refillEvery.
func NewMemoryLimiter(capacity int, refillEvery time.Duration) *MemoryLimiter {
	return &MemoryLimiter{
		capacity:    float64(capacity),
		refillEvery: refillEvery,
		now:         time.Now,
		buckets:     make(map[string]*bucket),
	}
}

// Allow implements LoginLimiter.
func (l *MemoryLimiter) Allow(_ context.Context, key string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return true, nil
	}
	l.refill(b)
	return b.tokens >= 1, nil
}

// Fail implements LoginLimiter.
func (l *MemoryLimiter) Fail(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: l.now()}
		l.buckets[key] = b
	}
	l.refill(b)
	if b.tokens >= 1 {
		b.tokens--
	}
	return nil
}

func (l *MemoryLimiter) refill(b *bucket) {
	now := l.now()
	b.tokens = min(l.capacity, b.tokens+float64(now.Sub(b.last))/float64(l.refillEvery))
	b.last = now
}

// sweep drops buckets that have refilled completely, so the map does not grow
// forever with one entry per attacker-chosen email. Runs at most once a minute.
func (l *MemoryLimiter) sweep() {
	now := l.now()
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		l.refill(b)
		if b.tokens >= l.capacity {
			delete(l.buckets, key)
		}
	}
}
