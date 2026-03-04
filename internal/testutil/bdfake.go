package testutil

import (
	"sync"
)

// Bead represents a minimal bead entry for testing.
type Bead struct {
	ID      string
	Subject string
	Status  string
	Labels  []string
}

// BDFake provides an in-memory fake for the beads CLI.
// Thread-safe: can be shared across goroutines.
type BDFake struct {
	mu    sync.Mutex
	beads map[string]*Bead
}

// NewBDFake creates a BDFake pre-populated with the given beads.
func NewBDFake(beads ...Bead) *BDFake {
	f := &BDFake{beads: make(map[string]*Bead)}
	for i := range beads {
		b := beads[i]
		f.beads[b.ID] = &b
	}
	return f
}

// ReadyCount returns the number of beads with status "open".
func (f *BDFake) ReadyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, b := range f.beads {
		if b.Status == "open" {
			count++
		}
	}
	return count
}

// Claim marks a bead as "in_progress". Returns false if the bead
// doesn't exist or is not "open".
func (f *BDFake) Claim(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.beads[id]
	if !ok || b.Status != "open" {
		return false
	}
	b.Status = "in_progress"
	return true
}

// Done marks a bead as "done". Returns false if the bead
// doesn't exist or is not "in_progress".
func (f *BDFake) Done(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.beads[id]
	if !ok || b.Status != "in_progress" {
		return false
	}
	b.Status = "done"
	return true
}

// Get returns a bead by ID. Returns nil if not found.
func (f *BDFake) Get(id string) *Bead {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.beads[id]
	if !ok {
		return nil
	}
	// Return a copy.
	copy := *b
	return &copy
}

// List returns all beads.
func (f *BDFake) List() []Bead {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]Bead, 0, len(f.beads))
	for _, b := range f.beads {
		result = append(result, *b)
	}
	return result
}

// Ready returns all beads with status "open".
func (f *BDFake) Ready() []Bead {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []Bead
	for _, b := range f.beads {
		if b.Status == "open" {
			result = append(result, *b)
		}
	}
	return result
}

// Add inserts a new bead.
func (f *BDFake) Add(b Bead) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beads[b.ID] = &b
}
