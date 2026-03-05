package termination

import (
	"strings"
	"testing"
)

func TestFixedTargetPrefersIterations(t *testing.T) {
	t.Parallel()

	iterations := 3
	max := 8
	cfg := FixedConfig{Iterations: &iterations, Max: &max}

	if got := cfg.Target(); got != 3 {
		t.Fatalf("Target() = %d, want 3", got)
	}
}

func TestFixedTargetFallsBackToMax(t *testing.T) {
	t.Parallel()

	max := 5
	cfg := FixedConfig{Max: &max}

	if got := cfg.Target(); got != 5 {
		t.Fatalf("Target() = %d, want 5", got)
	}
}

func TestFixedTargetDefaultsToOne(t *testing.T) {
	t.Parallel()

	cfg := FixedConfig{}
	if got := cfg.Target(); got != 1 {
		t.Fatalf("Target() = %d, want 1", got)
	}

	zero := 0
	cfg = FixedConfig{Iterations: &zero}
	if got := cfg.Target(); got != 1 {
		t.Fatalf("Target() = %d, want 1 for zero iterations", got)
	}
}

func TestFixedShouldStopOnDecisionStop(t *testing.T) {
	t.Parallel()

	strategy := NewFixed(FixedConfig{Max: intPtr(5)})

	done, reason := strategy.ShouldStop(2, "STOP")
	if !done {
		t.Fatalf("ShouldStop() = false, want true")
	}
	if !strings.Contains(reason, "requested stop") {
		t.Fatalf("reason = %q, want stop message", reason)
	}
}

func TestFixedShouldStopOnDecisionError(t *testing.T) {
	t.Parallel()

	strategy := NewFixed(FixedConfig{Max: intPtr(5)})

	done, reason := strategy.ShouldStop(2, "error")
	if !done {
		t.Fatalf("ShouldStop() = false, want true")
	}
	if !strings.Contains(reason, "reported error") {
		t.Fatalf("reason = %q, want error message", reason)
	}
}

func TestFixedShouldStopOnMaxIterations(t *testing.T) {
	t.Parallel()

	strategy := NewFixed(FixedConfig{Max: intPtr(3)})

	done, reason := strategy.ShouldStop(3, "")
	if !done {
		t.Fatalf("ShouldStop() = false, want true")
	}
	if !strings.Contains(reason, "Completed 3 iterations") {
		t.Fatalf("reason = %q, want completion message", reason)
	}
}

func TestFixedShouldContinueBeforeMax(t *testing.T) {
	t.Parallel()

	strategy := NewFixed(FixedConfig{Max: intPtr(3)})

	done, reason := strategy.ShouldStop(2, "")
	if done {
		t.Fatalf("ShouldStop() = true, want false")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func intPtr(value int) *int {
	return &value
}
