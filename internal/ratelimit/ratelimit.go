// Package ratelimit is a small in-memory token-bucket limiter keyed by an
// arbitrary string (e.g. client IP). Adequate for a single-instance prototype.
package ratelimit

import (
	"sync"
	"time"

	"smart-transit/internal/clock"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type Limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
	clock   clock.Clock
	lastGC  time.Time
}

// New allows `perMinute` requests per minute per key with bursts up to `burst`.
func New(perMinute, burst int, clk clock.Clock) *Limiter {
	return &Limiter{rate: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}, clock: clk}
}

// Allow consumes a token for key. When refused it returns how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()

	if now.Sub(l.lastGC) > 10*time.Minute { // drop idle, full buckets
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}
