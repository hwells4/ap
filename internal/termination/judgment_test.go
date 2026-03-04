package termination

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/judge"
	"github.com/hwells4/ap/internal/result"
)

// fakeJudge is a test double for judge.Judge.
type fakeJudge struct {
	verdicts []judge.Verdict
	errors   []error
	calls    int
}

func (f *fakeJudge) Evaluate(_ context.Context, _ judge.Request) (judge.Verdict, error) {
	idx := f.calls
	f.calls++
	if idx < len(f.errors) && f.errors[idx] != nil {
		return judge.Verdict{}, f.errors[idx]
	}
	if idx < len(f.verdicts) {
		return f.verdicts[idx], nil
	}
	return judge.Verdict{Decision: "continue", Confidence: 0.5, Rationale: "default"}, nil
}

func TestJudgment_ContinueDoesNotStop(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     1,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "continue", Confidence: 0.8, Rationale: "progress"},
	}}

	stop, reason := j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	if stop {
		t.Errorf("expected continue, got stop: %s", reason)
	}
	if j.ConsecutiveStops() != 0 {
		t.Errorf("consecutive stops = %d, want 0", j.ConsecutiveStops())
	}
}

func TestJudgment_SingleStopBelowConsensus(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     1,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.9, Rationale: "done"},
	}}

	stop, _ := j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	if stop {
		t.Error("single stop should not trigger termination with consensus=2")
	}
	if j.ConsecutiveStops() != 1 {
		t.Errorf("consecutive stops = %d, want 1", j.ConsecutiveStops())
	}
}

func TestJudgment_ConsensusReached_Stops(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     1,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.85, Rationale: "almost done"},
		{Decision: "stop", Confidence: 0.95, Rationale: "complete"},
	}}

	// First evaluation — 1 stop, not enough.
	stop1, _ := j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	if stop1 {
		t.Fatal("first stop should not trigger termination")
	}

	// Second evaluation — 2 consecutive stops, consensus reached.
	stop2, reason := j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if !stop2 {
		t.Fatal("expected stop after consensus reached")
	}
	if !strings.Contains(reason, "consensus") {
		t.Errorf("reason = %q, want consensus message", reason)
	}
}

func TestJudgment_ContinueResetsCounter(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     1,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.8, Rationale: "maybe"},
		{Decision: "continue", Confidence: 0.7, Rationale: "more to do"},
		{Decision: "stop", Confidence: 0.9, Rationale: "done now"},
	}}

	j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	if j.ConsecutiveStops() != 1 {
		t.Fatalf("after first stop: consecutive = %d, want 1", j.ConsecutiveStops())
	}

	j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if j.ConsecutiveStops() != 0 {
		t.Fatalf("after continue: consecutive = %d, want 0", j.ConsecutiveStops())
	}

	stop, _ := j.ShouldStop(context.Background(), 4, result.Result{}, fj, buildSummaries(4))
	if stop {
		t.Error("single stop after reset should not trigger termination")
	}
	if j.ConsecutiveStops() != 1 {
		t.Fatalf("after second stop: consecutive = %d, want 1", j.ConsecutiveStops())
	}
}

func TestJudgment_MinIterationsGate(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 1,
		MinIterations:     5,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.99, Rationale: "done"},
	}}

	// Below min iterations — should not stop even with consensus.
	stop, _ := j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if stop {
		t.Error("should not stop below min_iterations")
	}
}

func TestJudgment_MinIterationsGate_PassesWhenReached(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 1,
		MinIterations:     3,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.99, Rationale: "done"},
	}}

	stop, _ := j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if !stop {
		t.Error("should stop at min_iterations with consensus")
	}
}

func TestJudgment_JudgeFailure_FallbackAfterThree(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 1,
		MinIterations:     1,
	})
	fj := &fakeJudge{errors: []error{
		fmt.Errorf("fail 1"),
		fmt.Errorf("fail 2"),
		fmt.Errorf("fail 3"),
	}}

	// First two failures — no fallback yet, continue.
	j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if j.InFallback() {
		t.Error("should not be in fallback after 2 failures")
	}

	// Third failure — triggers fallback.
	j.ShouldStop(context.Background(), 4, result.Result{}, fj, buildSummaries(4))
	if !j.InFallback() {
		t.Error("should be in fallback after 3 consecutive failures")
	}
}

func TestJudgment_FallbackResetOnSuccess(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     1,
	})
	fj := &fakeJudge{
		errors: []error{fmt.Errorf("fail"), fmt.Errorf("fail"), nil},
		verdicts: []judge.Verdict{
			{}, {},
			{Decision: "continue", Confidence: 0.6, Rationale: "ok"},
		},
	}

	j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	if j.ConsecutiveFailures() != 2 {
		t.Fatalf("failures = %d, want 2", j.ConsecutiveFailures())
	}

	j.ShouldStop(context.Background(), 4, result.Result{}, fj, buildSummaries(4))
	if j.ConsecutiveFailures() != 0 {
		t.Fatalf("failures should reset after success, got %d", j.ConsecutiveFailures())
	}
}

func TestJudgment_DefaultConfig(t *testing.T) {
	j := NewJudgment(JudgmentConfig{})
	if j.consensusRequired != defaultConsensusRequired {
		t.Errorf("consensus = %d, want %d", j.consensusRequired, defaultConsensusRequired)
	}
	if j.minIterations != defaultMinIterations {
		t.Errorf("min_iterations = %d, want %d", j.minIterations, defaultMinIterations)
	}
}

func TestJudgment_InFallback_AlwaysReturnsFalse(t *testing.T) {
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 1,
		MinIterations:     1,
	})
	// Force into fallback via 3 failures.
	fj := &fakeJudge{errors: []error{
		fmt.Errorf("fail 1"),
		fmt.Errorf("fail 2"),
		fmt.Errorf("fail 3"),
	}}
	j.ShouldStop(context.Background(), 2, result.Result{}, fj, buildSummaries(2))
	j.ShouldStop(context.Background(), 3, result.Result{}, fj, buildSummaries(3))
	j.ShouldStop(context.Background(), 4, result.Result{}, fj, buildSummaries(4))
	if !j.InFallback() {
		t.Fatal("expected fallback after 3 failures")
	}

	// Now provide a stop verdict — should still return false because in fallback.
	fj2 := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.99, Rationale: "done"},
	}}
	stop, _ := j.ShouldStop(context.Background(), 5, result.Result{}, fj2, buildSummaries(5))
	if stop {
		t.Error("should not stop while in fallback mode")
	}
}

func TestJudgment_ConsensusNotReached_ExhaustsIterations(t *testing.T) {
	// Simulates that judgment can't prevent reaching max iterations:
	// alternate stop/continue so consensus is never reached.
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 3,
		MinIterations:     1,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "stop", Confidence: 0.8, Rationale: "maybe"},
		{Decision: "stop", Confidence: 0.85, Rationale: "close"},
		{Decision: "continue", Confidence: 0.6, Rationale: "not sure"},
		{Decision: "stop", Confidence: 0.9, Rationale: "done"},
		{Decision: "stop", Confidence: 0.9, Rationale: "done"},
	}}

	stopped := false
	for i := 1; i <= 5; i++ {
		s, _ := j.ShouldStop(context.Background(), i, result.Result{}, fj, buildSummaries(i))
		if s {
			stopped = true
			break
		}
	}
	if stopped {
		t.Error("should not stop — consensus of 3 never reached (reset by continue at iter 3)")
	}
}

func TestJudgment_ConsensusExactlyAtMinIterations(t *testing.T) {
	// Consensus reached exactly at min_iterations boundary.
	j := NewJudgment(JudgmentConfig{
		ConsensusRequired: 2,
		MinIterations:     4,
	})
	fj := &fakeJudge{verdicts: []judge.Verdict{
		{Decision: "continue", Confidence: 0.5, Rationale: "warming up"},
		{Decision: "continue", Confidence: 0.6, Rationale: "progressing"},
		{Decision: "stop", Confidence: 0.8, Rationale: "close"},
		{Decision: "stop", Confidence: 0.9, Rationale: "done"},
	}}

	var stoppedAt int
	for i := 1; i <= 6; i++ {
		s, _ := j.ShouldStop(context.Background(), i, result.Result{}, fj, buildSummaries(i))
		if s {
			stoppedAt = i
			break
		}
	}
	if stoppedAt != 4 {
		t.Errorf("stopped at iteration %d, want 4", stoppedAt)
	}
}

func buildSummaries(n int) []string {
	summaries := make([]string, n)
	for i := range summaries {
		summaries[i] = fmt.Sprintf("iteration %d work", i+1)
	}
	return summaries
}
