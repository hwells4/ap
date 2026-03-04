package termination

import (
	"context"
	"fmt"
	"sync"
)

// DefaultRaceAgents is the number of concurrent agents when not configured.
const DefaultRaceAgents = 2

// AcceptFirst means the first successful result wins.
const AcceptFirst = "first"

// RaceConfig captures race termination settings from stage.yaml.
type RaceConfig struct {
	Agents int    `json:"agents,omitempty" yaml:"agents,omitempty"`
	Accept string `json:"accept,omitempty" yaml:"accept,omitempty"`
}

// AgentCount returns the configured or default agent count.
func (c RaceConfig) AgentCount() int {
	if c.Agents > 0 {
		return c.Agents
	}
	return DefaultRaceAgents
}

// AcceptMode returns the configured or default accept mode.
func (c RaceConfig) AcceptMode() string {
	if c.Accept != "" {
		return c.Accept
	}
	return AcceptFirst
}

// RaceResult captures the output from one racing agent.
type RaceResult struct {
	AgentIndex int
	Output     any
	Err        error
}

// RaceRunner runs a function concurrently across N agents and returns the
// first successful result. Remaining agents are cancelled via context.
// The runner function receives a per-agent context that is cancelled when a
// winner is found and must return (result, error). A nil error means success.
type RaceRunner struct {
	config RaceConfig
}

// NewRaceRunner creates a RaceRunner from config.
func NewRaceRunner(cfg RaceConfig) *RaceRunner {
	return &RaceRunner{config: cfg}
}

// Config returns the runner's config.
func (r *RaceRunner) Config() RaceConfig {
	return r.config
}

// Run spawns N agents concurrently. The first successful result (err == nil)
// wins. All other agents are cancelled via context. If all agents fail, the
// last error is returned. The runner func receives agent index and a
// cancellable context.
func (r *RaceRunner) Run(ctx context.Context, fn func(ctx context.Context, agentIndex int) (any, error)) (RaceResult, error) {
	n := r.config.AgentCount()
	if n <= 0 {
		return RaceResult{}, fmt.Errorf("termination: race: agent count must be > 0")
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type indexedResult struct {
		index  int
		output any
		err    error
	}

	resultCh := make(chan indexedResult, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			output, err := fn(raceCtx, i)
			resultCh <- indexedResult{index: i, output: output, err: err}
		}()
	}

	// Close channel when all goroutines complete.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var lastErr error
	received := 0
	for res := range resultCh {
		received++
		if res.err == nil {
			// Winner found — cancel remaining agents.
			cancel()
			return RaceResult{
				AgentIndex: res.index,
				Output:     res.output,
			}, nil
		}
		lastErr = res.err
		if received >= n {
			break
		}
	}

	if lastErr != nil {
		return RaceResult{}, fmt.Errorf("termination: race: all %d agents failed: %w", n, lastErr)
	}
	return RaceResult{}, fmt.Errorf("termination: race: no results received")
}
