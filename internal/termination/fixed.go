// Package termination implements loop termination strategies.
package termination

import (
	"fmt"
	"strings"
)

// DefaultFixedIterations is the fallback iteration count when none is supplied.
const DefaultFixedIterations = 1

// FixedConfig captures fixed termination settings.
type FixedConfig struct {
	Iterations *int `json:"iterations,omitempty"`
	Max        *int `json:"max,omitempty"`
}

// Target returns the iteration cap for a fixed termination config.
func (c FixedConfig) Target() int {
	if c.Iterations != nil && *c.Iterations > 0 {
		return *c.Iterations
	}
	if c.Max != nil && *c.Max > 0 {
		return *c.Max
	}
	return DefaultFixedIterations
}

// Fixed enforces a fixed iteration limit with optional early stop decisions.
type Fixed struct {
	target int
}

// NewFixed builds a Fixed strategy from config.
func NewFixed(cfg FixedConfig) Fixed {
	return Fixed{target: cfg.Target()}
}

// Target returns the configured iteration cap.
func (f Fixed) Target() int {
	if f.target > 0 {
		return f.target
	}
	return DefaultFixedIterations
}

// ShouldStop determines whether the loop should terminate after this iteration.
// decision is the normalized decision string from the agent (continue/stop/error).
func (f Fixed) ShouldStop(iteration int, decision string) (bool, string) {
	hint := normalizeDecision(decision)
	if hint == "stop" {
		return true, fmt.Sprintf("Agent requested stop at iteration %d", iteration)
	}
	if hint == "error" {
		return true, fmt.Sprintf("Agent reported error at iteration %d", iteration)
	}

	target := f.Target()
	if iteration >= target {
		return true, fmt.Sprintf("Completed %d iterations (max: %d)", iteration, target)
	}
	return false, ""
}

func normalizeDecision(decision string) string {
	d := strings.ToLower(strings.TrimSpace(decision))
	switch d {
	case "stop", "error", "continue":
		return d
	default:
		return "continue"
	}
}
