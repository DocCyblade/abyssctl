package op

import (
	"sync"
	"time"
)

// Clock is the one source of the time for production code. This package is
// the only one allowed to call time.Now.
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock returns the real clock.
func SystemClock() Clock { return systemClock{} }

// ManualClock is a Clock for tests in any package: it returns the time it was
// set to and moves only on Set or Advance. It is safe for concurrent use.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock returns a ManualClock set to t.
func NewManualClock(t time.Time) *ManualClock { return &ManualClock{now: t} }

// Now returns the clock's current time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
