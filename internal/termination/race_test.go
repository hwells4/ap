package termination

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRaceConfig_Defaults(t *testing.T) {
	t.Parallel()
	cfg := RaceConfig{}
	if cfg.AgentCount() != DefaultRaceAgents {
		t.Fatalf("AgentCount() = %d, want %d", cfg.AgentCount(), DefaultRaceAgents)
	}
	if cfg.AcceptMode() != AcceptFirst {
		t.Fatalf("AcceptMode() = %q, want %q", cfg.AcceptMode(), AcceptFirst)
	}
}

func TestRaceConfig_CustomValues(t *testing.T) {
	t.Parallel()
	cfg := RaceConfig{Agents: 5, Accept: "best"}
	if cfg.AgentCount() != 5 {
		t.Fatalf("AgentCount() = %d, want 5", cfg.AgentCount())
	}
	if cfg.AcceptMode() != "best" {
		t.Fatalf("AcceptMode() = %q, want best", cfg.AcceptMode())
	}
}

func TestRaceRunner_FirstSuccessWins(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 3})

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		// Agent 0 succeeds immediately, others are slower.
		if idx == 0 {
			return "agent-0-output", nil
		}
		select {
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("should have been cancelled")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.AgentIndex != 0 {
		t.Fatalf("winner = agent %d, want 0", result.AgentIndex)
	}
	if result.Output != "agent-0-output" {
		t.Fatalf("output = %v, want agent-0-output", result.Output)
	}
}

func TestRaceRunner_CancelsRemainingAgents(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 3})

	var cancelled atomic.Int32

	_, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		if idx == 1 {
			return "winner", nil
		}
		<-ctx.Done()
		cancelled.Add(1)
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Give goroutines time to notice cancellation.
	time.Sleep(50 * time.Millisecond)

	if got := cancelled.Load(); got != 2 {
		t.Fatalf("cancelled agents = %d, want 2", got)
	}
}

func TestRaceRunner_AllAgentsFail(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 2})

	_, err := runner.Run(context.Background(), func(_ context.Context, idx int) (any, error) {
		return nil, fmt.Errorf("agent %d failed", idx)
	})
	if err == nil {
		t.Fatal("expected error when all agents fail")
	}
}

func TestRaceRunner_SlowWinnerStillAccepted(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 3})

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		// Agents 0 and 1 fail fast, agent 2 is slow but succeeds.
		if idx < 2 {
			return nil, fmt.Errorf("fail fast")
		}
		time.Sleep(30 * time.Millisecond)
		return "slow-winner", nil
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.AgentIndex != 2 {
		t.Fatalf("winner = agent %d, want 2", result.AgentIndex)
	}
}

func TestRaceRunner_ParentContextCancellation(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 2})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := runner.Run(ctx, func(ctx context.Context, _ int) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err == nil {
		t.Fatal("expected error when parent context is cancelled")
	}
}

func TestRaceRunner_SingleAgent(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 1})

	result, err := runner.Run(context.Background(), func(_ context.Context, _ int) (any, error) {
		return "solo", nil
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Output != "solo" {
		t.Fatalf("output = %v, want solo", result.Output)
	}
}
