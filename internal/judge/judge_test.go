package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/pkg/provider"
)

func TestJudge_ContinueVerdict(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: marshalVerdict(Verdict{
			Decision:   "continue",
			Confidence: 0.8,
			Rationale:  "Good progress",
		}),
	}))

	j := New(Config{Provider: prov})
	verdict, err := j.Evaluate(context.Background(), Request{
		Session:    "test-session",
		Stage:      "ralph",
		Iteration:  3,
		Summaries:  []string{"did A", "did B", "did C"},
		LastResult: "continue",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Decision != "continue" {
		t.Errorf("decision = %q, want continue", verdict.Decision)
	}
	if verdict.Confidence != 0.8 {
		t.Errorf("confidence = %v, want 0.8", verdict.Confidence)
	}
	if verdict.Rationale != "Good progress" {
		t.Errorf("rationale = %q, want 'Good progress'", verdict.Rationale)
	}
}

func TestJudge_StopVerdict(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: marshalVerdict(Verdict{
			Decision:   "stop",
			Confidence: 0.95,
			Rationale:  "Task complete",
		}),
	}))

	j := New(Config{Provider: prov})
	verdict, err := j.Evaluate(context.Background(), Request{
		Session:    "test-session",
		Stage:      "test",
		Iteration:  5,
		Summaries:  []string{"done"},
		LastResult: "stop",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Decision != "stop" {
		t.Errorf("decision = %q, want stop", verdict.Decision)
	}
	if verdict.Confidence != 0.95 {
		t.Errorf("confidence = %v, want 0.95", verdict.Confidence)
	}
}

func TestJudge_MalformedJSON_ReturnsError(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: "this is not json",
	}))

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"did stuff"},
	})
	if err == nil {
		t.Fatal("expected error for malformed output")
	}
}

func TestJudge_EmptyOutput_ReturnsError(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: "",
	}))

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"did stuff"},
	})
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestJudge_InvalidDecision_ReturnsError(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: marshalVerdict(Verdict{
			Decision:   "maybe",
			Confidence: 0.5,
			Rationale:  "unsure",
		}),
	}))

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"did stuff"},
	})
	if err == nil {
		t.Fatal("expected error for invalid decision")
	}
}

func TestJudge_ProviderError_ReturnsError(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.FailureResponse(fmt.Errorf("provider down"))))

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"did stuff"},
	})
	if err == nil {
		t.Fatal("expected error for provider failure")
	}
}

func TestJudge_RetryOnMalformed_SucceedsOnSecondAttempt(t *testing.T) {
	prov := mock.New(mock.WithResponses(
		mock.Response{Stdout: "garbage"},
		mock.Response{Stdout: marshalVerdict(Verdict{
			Decision:   "continue",
			Confidence: 0.7,
			Rationale:  "ok",
		})},
	))

	j := New(Config{Provider: prov, MaxRetries: 3})
	verdict, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 2,
		Summaries: []string{"did stuff"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Decision != "continue" {
		t.Errorf("decision = %q, want continue", verdict.Decision)
	}
	if prov.CallCount() != 2 {
		t.Errorf("call count = %d, want 2", prov.CallCount())
	}
}

func TestJudge_RetryExhausted_ReturnsError(t *testing.T) {
	prov := mock.New(mock.WithFallback(mock.Response{Stdout: "garbage"}))

	j := New(Config{Provider: prov, MaxRetries: 2})
	_, err := j.Evaluate(context.Background(), Request{
		Session:   "test-session",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"did stuff"},
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if prov.CallCount() != 2 {
		t.Errorf("call count = %d, want 2", prov.CallCount())
	}
}

func TestJudge_PromptIncludesHistory(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: marshalVerdict(Verdict{
			Decision:   "continue",
			Confidence: 0.6,
			Rationale:  "progress",
		}),
	}))

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(context.Background(), Request{
		Session:    "my-session",
		Stage:      "ralph",
		Iteration:  3,
		Summaries:  []string{"iteration 1 work", "iteration 2 work", "iteration 3 work"},
		LastResult: "continue",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	calls := prov.Calls()
	if len(calls) != 1 {
		t.Fatalf("call count = %d, want 1", len(calls))
	}
	prompt := calls[0].Request.Prompt
	for _, s := range []string{"my-session", "ralph", "iteration 1 work", "iteration 2 work", "iteration 3 work"} {
		if !containsString(prompt, s) {
			t.Errorf("prompt missing %q", s)
		}
	}
}

func TestJudge_DefaultRetries(t *testing.T) {
	j := New(Config{Provider: mock.New()})
	if j.maxRetries != defaultMaxRetries {
		t.Errorf("default retries = %d, want %d", j.maxRetries, defaultMaxRetries)
	}
}

func TestJudge_ContextCanceled(t *testing.T) {
	prov := mock.New(mock.WithResponses(mock.Response{
		Stdout: marshalVerdict(Verdict{Decision: "continue", Confidence: 0.5, Rationale: "ok"}),
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	j := New(Config{Provider: prov})
	_, err := j.Evaluate(ctx, Request{
		Session:   "test",
		Stage:     "test",
		Iteration: 1,
		Summaries: []string{"stuff"},
	})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// --- helpers ---

func marshalVerdict(v Verdict) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && contains(s, substr)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

var _ provider.Provider = (*mock.Provider)(nil)
