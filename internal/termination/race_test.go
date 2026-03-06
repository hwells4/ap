package termination

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// RaceConfig — unit tests
// ---------------------------------------------------------------------------

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

func TestRaceConfig_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cfg        RaceConfig
		wantAgents int
		wantAccept string
	}{
		{"zero agents uses default", RaceConfig{Agents: 0}, DefaultRaceAgents, AcceptFirst},
		{"negative agents uses default", RaceConfig{Agents: -1}, DefaultRaceAgents, AcceptFirst},
		{"one agent", RaceConfig{Agents: 1}, 1, AcceptFirst},
		{"large agent count", RaceConfig{Agents: 100}, 100, AcceptFirst},
		{"custom accept", RaceConfig{Accept: "majority"}, DefaultRaceAgents, "majority"},
		{"custom both", RaceConfig{Agents: 4, Accept: "consensus"}, 4, "consensus"},
		{"empty accept uses default", RaceConfig{Agents: 3, Accept: ""}, 3, AcceptFirst},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.AgentCount(); got != tc.wantAgents {
				t.Errorf("AgentCount() = %d, want %d", got, tc.wantAgents)
			}
			if got := tc.cfg.AcceptMode(); got != tc.wantAccept {
				t.Errorf("AcceptMode() = %q, want %q", got, tc.wantAccept)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewRaceRunner / Config — construction tests
// ---------------------------------------------------------------------------

func TestNewRaceRunner_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 3})
	if runner == nil {
		t.Fatal("NewRaceRunner() returned nil")
	}
}

func TestRaceRunner_Config(t *testing.T) {
	t.Parallel()
	cfg := RaceConfig{Agents: 7, Accept: "consensus"}
	runner := NewRaceRunner(cfg)
	got := runner.Config()
	if got.Agents != 7 {
		t.Errorf("Config().Agents = %d, want 7", got.Agents)
	}
	if got.Accept != "consensus" {
		t.Errorf("Config().Accept = %q, want consensus", got.Accept)
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — first success wins
// ---------------------------------------------------------------------------

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

func TestRaceRunner_FirstSuccessWins_AnyAgent(t *testing.T) {
	t.Parallel()
	// Verify that any agent can win — not just agent 0.
	for winnerIdx := 0; winnerIdx < 3; winnerIdx++ {
		winnerIdx := winnerIdx
		t.Run(fmt.Sprintf("winner_agent_%d", winnerIdx), func(t *testing.T) {
			t.Parallel()
			runner := NewRaceRunner(RaceConfig{Agents: 3})

			result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
				if idx == winnerIdx {
					return fmt.Sprintf("output-%d", idx), nil
				}
				select {
				case <-time.After(5 * time.Second):
					return nil, fmt.Errorf("timeout")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if result.AgentIndex != winnerIdx {
				t.Fatalf("winner = agent %d, want %d", result.AgentIndex, winnerIdx)
			}
			wantOutput := fmt.Sprintf("output-%d", winnerIdx)
			if result.Output != wantOutput {
				t.Fatalf("output = %v, want %s", result.Output, wantOutput)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — cancellation behavior
// ---------------------------------------------------------------------------

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

func TestRaceRunner_CancelsRemainingAgents_FiveAgents(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 5})

	var cancelled atomic.Int32

	_, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		if idx == 3 {
			return "winner", nil
		}
		<-ctx.Done()
		cancelled.Add(1)
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if got := cancelled.Load(); got != 4 {
		t.Fatalf("cancelled agents = %d, want 4", got)
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — all agents fail
// ---------------------------------------------------------------------------

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

func TestRaceRunner_AllAgentsFail_ErrorMessage(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 3})

	_, err := runner.Run(context.Background(), func(_ context.Context, idx int) (any, error) {
		return nil, fmt.Errorf("agent-%d-error", idx)
	})
	if err == nil {
		t.Fatal("expected error when all agents fail")
	}
	if !strings.Contains(err.Error(), "all 3 agents failed") {
		t.Fatalf("error = %q, want to contain 'all 3 agents failed'", err.Error())
	}
}

func TestRaceRunner_AllAgentsFail_WrapsLastError(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 2})

	_, err := runner.Run(context.Background(), func(_ context.Context, idx int) (any, error) {
		// Introduce delay so ordering is deterministic.
		time.Sleep(time.Duration(idx*10) * time.Millisecond)
		return nil, fmt.Errorf("error-%d", idx)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// The error should wrap the last received error.
	if !strings.Contains(err.Error(), "error-") {
		t.Fatalf("error = %q, expected to contain an agent error", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — slow winner accepted
// ---------------------------------------------------------------------------

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
	if result.Output != "slow-winner" {
		t.Fatalf("output = %v, want slow-winner", result.Output)
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — context cancellation
// ---------------------------------------------------------------------------

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

func TestRaceRunner_ParentContextTimeout(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 2})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := runner.Run(ctx, func(ctx context.Context, _ int) (any, error) {
		// All agents are slow — parent timeout fires first.
		select {
		case <-time.After(5 * time.Second):
			return "should not happen", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err == nil {
		t.Fatal("expected error when parent context times out")
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — single agent
// ---------------------------------------------------------------------------

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
	if result.AgentIndex != 0 {
		t.Fatalf("AgentIndex = %d, want 0", result.AgentIndex)
	}
}

func TestRaceRunner_SingleAgent_Fails(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 1})

	_, err := runner.Run(context.Background(), func(_ context.Context, _ int) (any, error) {
		return nil, fmt.Errorf("solo failure")
	})
	if err == nil {
		t.Fatal("expected error when single agent fails")
	}
	if !strings.Contains(err.Error(), "all 1 agents failed") {
		t.Fatalf("error = %q, want to contain 'all 1 agents failed'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — concurrent execution (integration-style)
// ---------------------------------------------------------------------------

func TestRaceRunner_AllAgentsRunConcurrently(t *testing.T) {
	t.Parallel()
	n := 5
	runner := NewRaceRunner(RaceConfig{Agents: n})

	// Use a barrier to verify all agents start concurrently.
	var started atomic.Int32
	barrier := make(chan struct{})

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		started.Add(1)
		// Wait until all agents have started (or timeout).
		if int(started.Load()) == n {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("barrier timeout")
		}
		// Agent 0 wins after barrier.
		if idx == 0 {
			return "concurrent-winner", nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Output != "concurrent-winner" {
		t.Fatalf("output = %v, want concurrent-winner", result.Output)
	}
	if got := started.Load(); got != int32(n) {
		t.Fatalf("started agents = %d, want %d (all should run concurrently)", got, n)
	}
}

func TestRaceRunner_MockProviders_ConcurrentExecution(t *testing.T) {
	t.Parallel()

	// Simulate mock providers: each "provider" does some work, one succeeds.
	type mockProvider struct {
		name     string
		latency  time.Duration
		succeed  bool
		response string
	}

	providers := []mockProvider{
		{name: "provider-A", latency: 50 * time.Millisecond, succeed: false},
		{name: "provider-B", latency: 20 * time.Millisecond, succeed: true, response: "B-result"},
		{name: "provider-C", latency: 80 * time.Millisecond, succeed: true, response: "C-result"},
	}

	runner := NewRaceRunner(RaceConfig{Agents: len(providers)})

	var mu sync.Mutex
	executionLog := make([]string, 0, len(providers))

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		p := providers[idx]
		select {
		case <-time.After(p.latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		mu.Lock()
		executionLog = append(executionLog, p.name)
		mu.Unlock()

		if !p.succeed {
			return nil, fmt.Errorf("%s failed", p.name)
		}
		return p.response, nil
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Provider B should win (fastest successful).
	if result.Output != "B-result" {
		t.Fatalf("output = %v, want B-result", result.Output)
	}
	if result.AgentIndex != 1 {
		t.Fatalf("AgentIndex = %d, want 1 (provider-B)", result.AgentIndex)
	}
	if result.Err != nil {
		t.Fatalf("Err = %v, want nil", result.Err)
	}
}

func TestRaceRunner_MockProviders_AllFail(t *testing.T) {
	t.Parallel()

	type mockProvider struct {
		name    string
		latency time.Duration
		errMsg  string
	}

	providers := []mockProvider{
		{name: "alpha", latency: 10 * time.Millisecond, errMsg: "alpha timeout"},
		{name: "beta", latency: 20 * time.Millisecond, errMsg: "beta rate limit"},
		{name: "gamma", latency: 30 * time.Millisecond, errMsg: "gamma 500"},
	}

	runner := NewRaceRunner(RaceConfig{Agents: len(providers)})

	_, err := runner.Run(context.Background(), func(_ context.Context, idx int) (any, error) {
		p := providers[idx]
		time.Sleep(p.latency)
		return nil, fmt.Errorf("%s", p.errMsg)
	})
	if err == nil {
		t.Fatal("expected error when all mock providers fail")
	}
	if !strings.Contains(err.Error(), "all 3 agents failed") {
		t.Fatalf("error = %q, want 'all 3 agents failed'", err.Error())
	}
}

func TestRaceRunner_MockProviders_MixedResultsTiming(t *testing.T) {
	t.Parallel()
	// Ensure that if the fastest agent fails, the second-fastest success still wins.
	runner := NewRaceRunner(RaceConfig{Agents: 4})

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		switch idx {
		case 0:
			// Fastest but fails.
			time.Sleep(5 * time.Millisecond)
			return nil, fmt.Errorf("fast failure")
		case 1:
			// Second fastest, succeeds.
			time.Sleep(15 * time.Millisecond)
			return "second-fastest-wins", nil
		case 2:
			// Slow, succeeds but should be cancelled.
			select {
			case <-time.After(500 * time.Millisecond):
				return "too-slow", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
			// Very slow.
			select {
			case <-time.After(1 * time.Second):
				return nil, fmt.Errorf("very slow")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Output != "second-fastest-wins" {
		t.Fatalf("output = %v, want second-fastest-wins", result.Output)
	}
	if result.AgentIndex != 1 {
		t.Fatalf("AgentIndex = %d, want 1", result.AgentIndex)
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — result struct validation
// ---------------------------------------------------------------------------

func TestRaceResult_SuccessHasNilErr(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 1})

	result, err := runner.Run(context.Background(), func(_ context.Context, _ int) (any, error) {
		return "data", nil
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result.Err = %v, want nil", result.Err)
	}
	if result.Output != "data" {
		t.Fatalf("result.Output = %v, want data", result.Output)
	}
}

func TestRaceResult_StructuredOutput(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 2})

	type payload struct {
		Code    int
		Message string
	}

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		if idx == 0 {
			return payload{Code: 200, Message: "ok"}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	p, ok := result.Output.(payload)
	if !ok {
		t.Fatalf("output type = %T, want payload", result.Output)
	}
	if p.Code != 200 || p.Message != "ok" {
		t.Fatalf("output = %+v, want {200, ok}", p)
	}
}

// ---------------------------------------------------------------------------
// RaceRunner.Run — edge cases
// ---------------------------------------------------------------------------

func TestRaceRunner_NilOutput_Success(t *testing.T) {
	t.Parallel()
	runner := NewRaceRunner(RaceConfig{Agents: 1})

	result, err := runner.Run(context.Background(), func(_ context.Context, _ int) (any, error) {
		return nil, nil // nil output is still a success
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.Output != nil {
		t.Fatalf("output = %v, want nil", result.Output)
	}
}

func TestRaceRunner_LargeAgentCount(t *testing.T) {
	t.Parallel()
	n := 20
	runner := NewRaceRunner(RaceConfig{Agents: n})

	var started atomic.Int32

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		started.Add(1)
		if idx == n-1 {
			// Last agent wins.
			return "last-agent", nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.AgentIndex != n-1 {
		t.Fatalf("winner = agent %d, want %d", result.AgentIndex, n-1)
	}
}

func TestRaceRunner_DefaultAgentCount(t *testing.T) {
	t.Parallel()
	// Using default config (no Agents set) should use DefaultRaceAgents.
	runner := NewRaceRunner(RaceConfig{})

	var started atomic.Int32

	result, err := runner.Run(context.Background(), func(ctx context.Context, idx int) (any, error) {
		started.Add(1)
		if idx == 0 {
			return "default-winner", nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	if got := started.Load(); got != int32(DefaultRaceAgents) {
		t.Fatalf("started agents = %d, want %d (DefaultRaceAgents)", got, DefaultRaceAgents)
	}
	if result.Output != "default-winner" {
		t.Fatalf("output = %v, want default-winner", result.Output)
	}
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestConstants(t *testing.T) {
	t.Parallel()
	if DefaultRaceAgents != 2 {
		t.Fatalf("DefaultRaceAgents = %d, want 2", DefaultRaceAgents)
	}
	if AcceptFirst != "first" {
		t.Fatalf("AcceptFirst = %q, want first", AcceptFirst)
	}
}
