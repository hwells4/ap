package testutil

import (
	"testing"
	"time"
)

// Verify both clocks satisfy the Clock interface at compile time.
var (
	_ Clock = RealClock{}
	_ Clock = (*FakeClock)(nil)
)

func TestRealClock_Now(t *testing.T) {
	t.Parallel()
	c := RealClock{}
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestRealClock_Since(t *testing.T) {
	t.Parallel()
	c := RealClock{}
	start := time.Now()
	time.Sleep(time.Millisecond)
	d := c.Since(start)
	if d < time.Millisecond {
		t.Errorf("Since() = %v, want >= 1ms", d)
	}
}

func TestFakeClock_Defaults(t *testing.T) {
	t.Parallel()
	c := NewFakeClock(time.Time{})
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := c.Now(); !got.Equal(want) {
		t.Errorf("Now() = %v, want %v", got, want)
	}
}

func TestFakeClock_CustomStart(t *testing.T) {
	t.Parallel()
	start := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	if got := c.Now(); !got.Equal(start) {
		t.Errorf("Now() = %v, want %v", got, start)
	}
}

func TestFakeClock_Advance(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)

	c.Advance(5 * time.Second)
	want := start.Add(5 * time.Second)
	if got := c.Now(); !got.Equal(want) {
		t.Errorf("after Advance(5s): Now() = %v, want %v", got, want)
	}

	c.Advance(10 * time.Minute)
	want = want.Add(10 * time.Minute)
	if got := c.Now(); !got.Equal(want) {
		t.Errorf("after Advance(10m): Now() = %v, want %v", got, want)
	}
}

func TestFakeClock_Set(t *testing.T) {
	t.Parallel()
	c := NewFakeClock(time.Time{})
	target := time.Date(2030, 12, 25, 8, 0, 0, 0, time.UTC)
	c.Set(target)
	if got := c.Now(); !got.Equal(target) {
		t.Errorf("after Set: Now() = %v, want %v", got, target)
	}
}

func TestFakeClock_Since(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	c.Advance(30 * time.Second)

	d := c.Since(start)
	if d != 30*time.Second {
		t.Errorf("Since() = %v, want 30s", d)
	}
}

func TestFakeClock_Sleep(t *testing.T) {
	t.Parallel()
	c := NewFakeClock(time.Time{})
	before := c.Now()
	c.Sleep(time.Hour) // Should be no-op.
	after := c.Now()
	if !before.Equal(after) {
		t.Error("Sleep() should not advance time")
	}
}

func TestFakeClock_NewTimer(t *testing.T) {
	t.Parallel()
	c := NewFakeClock(time.Time{})
	timer := c.NewTimer(time.Hour)
	defer timer.Stop()

	// Should fire immediately.
	select {
	case <-timer.C:
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Error("NewTimer should fire immediately in fake clock")
	}
}
