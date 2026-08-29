package auth

import (
	"sync"
	"time"
)

// RateLimiter is an in-memory sliding-window rate limiter keyed by string.
// It exists to stop low-effort abuse (login brute force, email flooding),
// not to shape traffic precisely. State is per-process; with a single
// instance (the deployment model of elyfeed) that is the whole system.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
}

// NewRateLimiter returns an empty limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string][]time.Time)}
}

// Allow reports whether an action for key is permitted (fewer than limit
// actions inside the window) and, if so, records it.
func (l *RateLimiter) Allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	cutoff := now.Add(-window)
	l.mu.Lock()
	defer l.mu.Unlock()

	recent := l.buckets[key][:0]
	for _, t := range l.buckets[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		l.buckets[key] = recent
		return false
	}
	l.buckets[key] = append(recent, now)
	return true
}

// Prune drops all entries older than maxWindow, keeping memory bounded.
// Call it periodically (the auth service does this on a ticker).
func (l *RateLimiter) Prune(maxWindow time.Duration) {
	cutoff := time.Now().Add(-maxWindow)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, ts := range l.buckets {
		recent := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.buckets, k)
		} else {
			l.buckets[k] = recent
		}
	}
}
