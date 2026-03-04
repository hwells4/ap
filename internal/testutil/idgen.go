package testutil

import (
	"fmt"
	"sync"
)

// IDGen produces deterministic sequential IDs for testing.
// Thread-safe: can be shared across goroutines.
type IDGen struct {
	mu     sync.Mutex
	prefix string
	next   int
}

// NewIDGen creates an IDGen with the given prefix.
// IDs are formatted as "{prefix}-001", "{prefix}-002", etc.
func NewIDGen(prefix string) *IDGen {
	return &IDGen{prefix: prefix, next: 1}
}

// Next returns the next sequential ID.
func (g *IDGen) Next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := fmt.Sprintf("%s-%03d", g.prefix, g.next)
	g.next++
	return id
}

// Peek returns the next ID without advancing the counter.
func (g *IDGen) Peek() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return fmt.Sprintf("%s-%03d", g.prefix, g.next)
}

// Reset restarts the counter from 1.
func (g *IDGen) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next = 1
}

// Count returns the number of IDs generated so far.
func (g *IDGen) Count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.next - 1
}
