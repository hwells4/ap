package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

func TestRun_RetryOnProviderFailure_SucceedsAfterRetry(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithResponses(
		mock.FailureResponse(fmt.Errorf("transient API error")),
		mock.FailureResponse(fmt.Errorf("transient API error")),
		mock.ContinueResponse("recovered"),
	))

	res, err := Run(context.Background(), Config{
		Session:          "retry-success",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 3,
		RetryBackoff:     1 * time.Millisecond,
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Errorf("status = %v, want completed", res.Status)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if mp.CallCount() != 3 {
		t.Errorf("call count = %d, want 3", mp.CallCount())
	}
}

func TestRun_RetryExhausted_Aborts(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithFallback(
		mock.FailureResponse(fmt.Errorf("persistent error")),
	))

	res, err := Run(context.Background(), Config{
		Session:          "retry-exhaust-abort",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 3,
		RetryBackoff:     1 * time.Millisecond,
		RetryOnExhausted: "abort",
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %v, want failed", res.Status)
	}
	if mp.CallCount() != 3 {
		t.Errorf("call count = %d, want 3", mp.CallCount())
	}
}

func TestRun_RetryExhausted_Pauses(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithFallback(
		mock.FailureResponse(fmt.Errorf("persistent error")),
	))

	res, err := Run(context.Background(), Config{
		Session:          "retry-exhaust-pause",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 2,
		RetryBackoff:     1 * time.Millisecond,
		RetryOnExhausted: "pause",
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusPaused {
		t.Errorf("status = %v, want paused", res.Status)
	}
}

func TestRun_RetryEmitsEvents(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithResponses(
		mock.FailureResponse(fmt.Errorf("fail 1")),
		mock.ContinueResponse("ok"),
	))

	_, err := Run(context.Background(), Config{
		Session:          "retry-events",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 3,
		RetryBackoff:     1 * time.Millisecond,
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "retry-events")
	retries := filterByType(evts, store.TypeIterationRetried)
	if len(retries) != 1 {
		t.Errorf("iteration.retried events = %d, want 1", len(retries))
	}
	if len(retries) > 0 {
		data := parseEventData(t, retries[0])
		if data["attempt"] == nil {
			t.Error("retry event missing 'attempt' field")
		}
		if data["error"] == nil {
			t.Error("retry event missing 'error' field")
		}
	}
}

func TestRun_NoRetry_DefaultBehavior(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithResponses(
		mock.FailureResponse(fmt.Errorf("immediate fail")),
	))

	res, err := Run(context.Background(), Config{
		Session:        "no-retry",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %v, want failed", res.Status)
	}
	if mp.CallCount() != 1 {
		t.Errorf("call count = %d, want 1 (no retry)", mp.CallCount())
	}
}

func TestRun_RetryRespectsContextCancellation(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithFallback(
		mock.FailureResponse(fmt.Errorf("will keep failing")),
	))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	res, err := Run(ctx, Config{
		Session:          "retry-cancel",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 10,
		RetryBackoff:     500 * time.Millisecond,
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %v, want failed", res.Status)
	}
	if mp.CallCount() >= 10 {
		t.Errorf("call count = %d, expected < 10 due to cancellation", mp.CallCount())
	}
}

func TestRun_RetryBackoffEventData(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(mock.WithResponses(
		mock.FailureResponse(fmt.Errorf("fail")),
		mock.FailureResponse(fmt.Errorf("fail again")),
		mock.ContinueResponse("ok"),
	))

	_, err := Run(context.Background(), Config{
		Session:          "retry-backoff-data",
		RunDir:           runDir,
		StageName:        "test-stage",
		Provider:         mp,
		Iterations:       1,
		PromptTemplate:   "iteration ${ITERATION}",
		RetryMaxAttempts: 3,
		RetryBackoff:     1 * time.Millisecond,
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "retry-backoff-data")
	retries := filterByType(evts, store.TypeIterationRetried)
	if len(retries) != 2 {
		t.Fatalf("iteration.retried events = %d, want 2", len(retries))
	}

	for idx, retry := range retries {
		data := parseEventData(t, retry)
		var n int
		switch v := data["attempt"].(type) {
		case float64:
			n = int(v)
		case json.Number:
			n64, _ := v.Int64()
			n = int(n64)
		default:
			t.Errorf("retry[%d] attempt unexpected type: %T", idx, data["attempt"])
			continue
		}
		if n != idx+1 {
			t.Errorf("retry[%d] attempt = %d, want %d", idx, n, idx+1)
		}
	}
}
