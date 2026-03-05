package termination

import (
	"context"
	"fmt"

	"github.com/hwells4/ap/internal/judge"
)

const (
	defaultConsensusRequired  = 2
	defaultMinIterations      = 3
	defaultMaxJudgeFailures   = 3
)

// Evaluator abstracts judge.Judge for testing.
type Evaluator interface {
	Evaluate(ctx context.Context, req judge.Request) (judge.Verdict, error)
}

// JudgmentConfig captures judgment termination settings.
type JudgmentConfig struct {
	// ConsensusRequired is the number of consecutive "stop" verdicts needed.
	// Zero or negative uses the default (2).
	ConsensusRequired int

	// MinIterations is the minimum iteration count before stop is allowed.
	// Zero or negative uses the default (3).
	MinIterations int
}

// Judgment implements consensus-based termination using a judge model.
// It requires N consecutive "stop" judgments before terminating, respects
// a minimum iteration gate, and falls back to fixed-iteration behavior
// after repeated judge failures.
type Judgment struct {
	consensusRequired  int
	minIterations      int
	consecutiveStops   int
	consecutiveFailures int
	fallback           bool
}

// NewJudgment builds a Judgment strategy from config.
func NewJudgment(cfg JudgmentConfig) *Judgment {
	consensus := cfg.ConsensusRequired
	if consensus <= 0 {
		consensus = defaultConsensusRequired
	}
	minIter := cfg.MinIterations
	if minIter <= 0 {
		minIter = defaultMinIterations
	}
	return &Judgment{
		consensusRequired: consensus,
		minIterations:     minIter,
	}
}

// ShouldStop evaluates whether the loop should stop based on judge consensus.
// It returns (true, reason) when consensus is reached and min iterations are met,
// or (false, "") to continue.
func (j *Judgment) ShouldStop(
	ctx context.Context,
	iteration int,
	evaluator Evaluator,
	summaries []string,
) (bool, string) {
	if j.fallback {
		return false, ""
	}

	verdict, err := evaluator.Evaluate(ctx, judge.Request{
		Iteration: iteration,
		Summaries: summaries,
	})
	if err != nil {
		j.consecutiveFailures++
		if j.consecutiveFailures >= defaultMaxJudgeFailures {
			j.fallback = true
		}
		return false, ""
	}

	// Success resets failure counter.
	j.consecutiveFailures = 0

	if verdict.Decision == "stop" {
		j.consecutiveStops++
	} else {
		j.consecutiveStops = 0
	}

	if j.consecutiveStops >= j.consensusRequired && iteration >= j.minIterations {
		return true, fmt.Sprintf(
			"Judge consensus reached (%d consecutive stop verdicts, confidence %.2f): %s",
			j.consecutiveStops, verdict.Confidence, verdict.Rationale,
		)
	}

	return false, ""
}

// ConsecutiveStops returns the current consecutive stop count.
func (j *Judgment) ConsecutiveStops() int {
	return j.consecutiveStops
}

// ConsecutiveFailures returns the current consecutive judge failure count.
func (j *Judgment) ConsecutiveFailures() int {
	return j.consecutiveFailures
}

// InFallback returns true if the judge has failed too many times and
// the strategy has fallen back to fixed-iteration behavior.
func (j *Judgment) InFallback() bool {
	return j.fallback
}
