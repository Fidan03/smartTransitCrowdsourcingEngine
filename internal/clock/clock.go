// Package clock abstracts the current time so time-based rules (stale GPS,
// the 10-minute alert window) can be tested without real waiting.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

// Real returns the system time in UTC.
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake is a manually controlled clock for tests.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t.UTC()
}
