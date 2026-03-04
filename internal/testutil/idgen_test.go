package testutil

import (
	"sync"
	"testing"
)

func TestIDGen_Sequential(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("test")

	want := []string{"test-001", "test-002", "test-003"}
	for i, expected := range want {
		got := gen.Next()
		if got != expected {
			t.Errorf("Next() call %d = %q, want %q", i+1, got, expected)
		}
	}
}

func TestIDGen_CustomPrefix(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("sig")
	if got := gen.Next(); got != "sig-001" {
		t.Errorf("Next() = %q, want %q", got, "sig-001")
	}
}

func TestIDGen_Peek(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("evt")

	peek := gen.Peek()
	if peek != "evt-001" {
		t.Errorf("Peek() = %q, want %q", peek, "evt-001")
	}

	// Peek should not advance.
	next := gen.Next()
	if next != "evt-001" {
		t.Errorf("Next() after Peek = %q, want %q", next, "evt-001")
	}
}

func TestIDGen_Reset(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("test")
	gen.Next()
	gen.Next()

	gen.Reset()
	if got := gen.Next(); got != "test-001" {
		t.Errorf("Next() after Reset = %q, want %q", got, "test-001")
	}
}

func TestIDGen_Count(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("test")

	if got := gen.Count(); got != 0 {
		t.Errorf("Count() initial = %d, want 0", got)
	}

	gen.Next()
	gen.Next()
	gen.Next()

	if got := gen.Count(); got != 3 {
		t.Errorf("Count() after 3 calls = %d, want 3", got)
	}
}

func TestIDGen_Concurrent(t *testing.T) {
	t.Parallel()
	gen := NewIDGen("conc")
	n := 100

	var wg sync.WaitGroup
	ids := make([]string, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ids[idx] = gen.Next()
		}(i)
	}
	wg.Wait()

	// All IDs should be unique.
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID: %q", id)
		}
		seen[id] = true
	}

	if gen.Count() != n {
		t.Errorf("Count() = %d, want %d", gen.Count(), n)
	}
}
