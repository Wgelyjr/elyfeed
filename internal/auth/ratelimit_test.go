package auth

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	l := NewRateLimiter()
	for i := 0; i < 5; i++ {
		if !l.Allow("key", 5, time.Minute) {
			t.Fatalf("attempt %d: want allowed", i+1)
		}
	}
	if l.Allow("key", 5, time.Minute) {
		t.Fatal("attempt 6: want rate limited")
	}
	// Other keys are independent.
	if !l.Allow("other", 5, time.Minute) {
		t.Fatal("other key: want allowed")
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	l := NewRateLimiter()
	for i := 0; i < 3; i++ {
		l.Allow("key", 3, 10*time.Millisecond)
	}
	if l.Allow("key", 3, 10*time.Millisecond) {
		t.Fatal("want rate limited")
	}
	time.Sleep(25 * time.Millisecond)
	l.Prune(10 * time.Millisecond)
	if !l.Allow("key", 3, 10*time.Millisecond) {
		t.Fatal("after window expired: want allowed")
	}
}

func TestRateLimiterPrune(t *testing.T) {
	l := NewRateLimiter()
	l.Allow("key", 100, time.Hour)
	if len(l.buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(l.buckets))
	}

	// A fresh entry survives pruning.
	l.Prune(time.Hour)
	if len(l.buckets) != 1 {
		t.Fatal("fresh entry pruned")
	}

	// Age the entry directly and prune it out.
	l.mu.Lock()
	l.buckets["key"] = []time.Time{time.Now().Add(-2 * time.Hour)}
	l.mu.Unlock()
	l.Prune(time.Hour)
	if len(l.buckets) != 0 {
		t.Fatalf("stale entry not pruned: %v", l.buckets)
	}
}

// TestRateLimiterConcurrent hammers the limiter from many goroutines to
// check for data races and that the limit holds.
func TestRateLimiterConcurrent(t *testing.T) {
	l := NewRateLimiter()
	const goroutines = 16
	const limit = 10
	allowed := make(chan bool, goroutines*20)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				allowed <- l.Allow("shared", limit, time.Minute)
			}
		}()
	}
	wg.Wait()
	close(allowed)
	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	if count != limit {
		t.Fatalf("allowed %d requests, want exactly %d", count, limit)
	}
}
