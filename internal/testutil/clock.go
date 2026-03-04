// Package testutil provides test infrastructure for the ap project.
//
// Clock provides deterministic time injection. IDGen produces sequential IDs.
// TempSession scaffolds session directories. BDFake fakes the beads CLI.
// FakeProviderBin compiles a test binary for process-boundary tests.
package testutil

import (
	"sync"
	"time"
)

// Clock abstracts time operations for deterministic testing.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
	NewTimer(time.Duration) *time.Timer
	Sleep(time.Duration)
}

// RealClock uses the real system clock.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// Since returns the elapsed duration since t.
func (RealClock) Since(t time.Time) time.Duration { return time.Since(t) }

// NewTimer creates a real timer.
func (RealClock) NewTimer(d time.Duration) *time.Timer { return time.NewTimer(d) }

// Sleep blocks for the specified duration.
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// FakeClock provides deterministic time for tests.
// All operations are thread-safe.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock creates a FakeClock starting at the given time.
// If zero, defaults to 2026-01-01T00:00:00Z.
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &FakeClock{now: start}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns the duration from t to the current fake time.
func (c *FakeClock) Since(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(t)
}

// NewTimer creates a timer that fires immediately (tests should not wait).
func (c *FakeClock) NewTimer(_ time.Duration) *time.Timer {
	t := time.NewTimer(0)
	return t
}

// Sleep is a no-op in the fake clock (tests should not wait).
func (c *FakeClock) Sleep(_ time.Duration) {}

// Advance moves the fake clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set moves the fake clock to a specific time.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
