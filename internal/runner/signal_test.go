package runner

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

// TestIntegration_SignalCancelsIteration verifies that context cancellation
// (simulating SIGINT/SIGTERM via WithSignalHandling) stops provider execution
// and records graceful termination metadata in state and events.
func TestIntegration_SignalCancelsIteration(t *testing.T) {
	runDir := tempSession(t)

	// Provider that blocks long enough for us to cancel.
	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "working",
			Delay:    10 * time.Second,
		}),
	)

	cfg := Config{
		Session:        "test-signal",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     100,
		PromptTemplate: "iteration ${ITERATION}",
	}

	// Cancel after a short delay to simulate signal arrival.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Session should be failed due to signal/context cancellation.
	if res.Status != state.StateFailed {
		t.Errorf("status = %q, want %q", res.Status, state.StateFailed)
	}

	// Error should mention context.
	if res.Error == "" {
		t.Error("expected non-empty error")
	}

	// Verify state.json shows failed.
	statePath := runDir + "/state.json"
	st, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StateFailed {
		t.Errorf("state status = %q, want %q", st.Status, state.StateFailed)
	}

	// Verify events.jsonl has error/termination events.
	eventsData, err := os.ReadFile(runDir + "/events.jsonl")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	events := strings.Split(strings.TrimSpace(string(eventsData)), "\n")
	if len(events) == 0 {
		t.Fatal("no events recorded")
	}

	// Should have at least session.started and a failure/error event.
	hasSessionStart := false
	hasFailureEvent := false
	for _, line := range events {
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		switch evt["type"] {
		case "session.started":
			hasSessionStart = true
		case "error", "iteration.failed":
			hasFailureEvent = true
		}
	}
	if !hasSessionStart {
		t.Error("missing session.started event")
	}
	if !hasFailureEvent {
		t.Error("missing failure event for signal termination")
	}
}

// TestIntegration_SignalDuringSecondIteration verifies that a signal
// arriving between iterations (after one completes) properly records
// the completed iteration count.
func TestIntegration_SignalDuringSecondIteration(t *testing.T) {
	runDir := tempSession(t)

	// First iteration completes fast, second blocks.
	mp := mock.New(
		mock.WithResponses(
			mock.Response{
				Decision: "continue",
				Summary:  "first done",
			},
			mock.Response{
				Decision: "continue",
				Summary:  "will be cancelled",
				Delay:    10 * time.Second,
			},
		),
	)

	cfg := Config{
		Session:        "test-signal-mid",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     10,
		PromptTemplate: "iteration ${ITERATION}",
	}

	// Allow enough time for first iteration but cancel during second.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != state.StateFailed {
		t.Errorf("status = %q, want %q", res.Status, state.StateFailed)
	}

	// First iteration should have completed before signal.
	// Provider should have been called at least once (first iteration)
	// and possibly a second time (cancelled mid-execution).
	calls := mp.CallCount()
	if calls < 1 {
		t.Errorf("provider calls = %d, want >= 1", calls)
	}

	// State should show at least iteration 1 completed.
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.IterationCompleted < 1 {
		t.Errorf("iteration_completed = %d, want >= 1", st.IterationCompleted)
	}
}

// TestWithSignalHandling_ContextCancel verifies WithSignalHandling returns
// a context that can be cancelled normally (without actual signals).
func TestWithSignalHandling_ContextCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := WithSignalHandling(parent)
	defer cancel()

	// Cancel the parent — child should also be done.
	parentCancel()

	select {
	case <-ctx.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("context not cancelled within 1s")
	}
}
