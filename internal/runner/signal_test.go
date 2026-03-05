package runner

import (
	"context"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestIntegration_SignalCancelsIteration verifies that context cancellation
// stops provider execution and records graceful termination metadata.
func TestIntegration_SignalCancelsIteration(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, store.StatusFailed)
	}
	if res.Error == "" {
		t.Error("expected non-empty error")
	}

	// Verify store session shows failed.
	row, err := s.GetSession(context.Background(), "test-signal")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusFailed {
		t.Errorf("store status = %q, want %q", row.Status, store.StatusFailed)
	}

	// Verify events have at least session.started and a failure event.
	evts := readEvents(t, s, "test-signal")
	hasSessionStart := false
	hasFailureEvent := false
	for _, evt := range evts {
		switch evt.Type {
		case store.TypeSessionStart:
			hasSessionStart = true
		case store.TypeError, store.TypeIterationFailed:
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
// arriving between iterations properly records the completed iteration count.
func TestIntegration_SignalDuringSecondIteration(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, store.StatusFailed)
	}

	calls := mp.CallCount()
	if calls < 1 {
		t.Errorf("provider calls = %d, want >= 1", calls)
	}

	row, err := s.GetSession(context.Background(), "test-signal-mid")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.IterationCompleted < 1 {
		t.Errorf("iteration_completed = %d, want >= 1", row.IterationCompleted)
	}
}

// TestWithSignalHandling_ContextCancel verifies WithSignalHandling returns
// a context that can be cancelled normally.
func TestWithSignalHandling_ContextCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := WithSignalHandling(parent)
	defer cancel()

	parentCancel()

	select {
	case <-ctx.Done():
		// Expected.
	case <-time.After(time.Second):
		t.Fatal("context not cancelled within 1s")
	}
}
