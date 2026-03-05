package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/pkg/provider"
)

// Verify MockProvider satisfies the Provider interface at compile time.
var _ provider.Provider = (*Provider)(nil)

func TestNew_Defaults(t *testing.T) {
	p := New()
	if p.Name() != "mock" {
		t.Errorf("Name() = %q, want %q", p.Name(), "mock")
	}
	if p.DefaultModel() != "mock-default" {
		t.Errorf("DefaultModel() = %q, want %q", p.DefaultModel(), "mock-default")
	}
}

func TestNew_WithOptions(t *testing.T) {
	p := New(
		WithName("test-provider"),
		WithModel("test-model"),
	)
	if p.Name() != "test-provider" {
		t.Errorf("Name() = %q, want %q", p.Name(), "test-provider")
	}
	if p.DefaultModel() != "test-model" {
		t.Errorf("DefaultModel() = %q, want %q", p.DefaultModel(), "test-model")
	}
}

func TestNew_EmptyName(t *testing.T) {
	p := New(WithName(""))
	if p.Name() != "mock" {
		t.Errorf("Name() = %q, want %q (empty should keep default)", p.Name(), "mock")
	}
}

func TestInitShutdown(t *testing.T) {
	p := New()
	ctx := context.Background()

	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !p.initialized {
		t.Error("Init() did not set initialized = true")
	}

	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if p.initialized {
		t.Error("Shutdown() did not set initialized = false")
	}
}

func TestValidate(t *testing.T) {
	p := New()
	if err := p.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestCapabilities(t *testing.T) {
	p := New(WithModel("test-model"))
	caps := p.Capabilities()

	if caps.Flags != provider.CapabilityNone {
		t.Errorf("Flags = %d, want %d", caps.Flags, provider.CapabilityNone)
	}
	if len(caps.SupportedModels) != 1 || caps.SupportedModels[0] != "test-model" {
		t.Errorf("SupportedModels = %v, want [test-model]", caps.SupportedModels)
	}
}

func TestExecute_DefaultResponse(t *testing.T) {
	p := New()
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test prompt"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Model != "mock-default" {
		t.Errorf("Model = %q, want %q", result.Model, "mock-default")
	}
	if p.CallCount() != 1 {
		t.Errorf("CallCount() = %d, want 1", p.CallCount())
	}
	// Default response emits an ap-result block.
	if !strings.Contains(result.Stdout, "```ap-result") {
		t.Error("default response should emit ap-result block")
	}
}

func TestExecute_PerIterationResponses(t *testing.T) {
	p := New(WithResponses(
		ContinueResponse("did step 1"),
		ContinueResponse("did step 2"),
		StopResponse("all done", "work complete"),
	))
	ctx := context.Background()

	for i, want := range []struct {
		decision string
		summary  string
	}{
		{"continue", "did step 1"},
		{"continue", "did step 2"},
		{"stop", "all done"},
	} {
		result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
		if err != nil {
			t.Fatalf("iteration %d: Execute() error = %v", i+1, err)
		}
		if result.ExitCode != 0 {
			t.Errorf("iteration %d: ExitCode = %d, want 0", i+1, result.ExitCode)
		}

		dec := extractDecisionFromStdout(t, result.Stdout)
		if dec["decision"] != want.decision {
			t.Errorf("iteration %d: decision = %q, want %q", i+1, dec["decision"], want.decision)
		}
		if dec["summary"] != want.summary {
			t.Errorf("iteration %d: summary = %q, want %q", i+1, dec["summary"], want.summary)
		}
	}

	if p.CallCount() != 3 {
		t.Errorf("CallCount() = %d, want 3", p.CallCount())
	}
}

func TestExecute_Fallback(t *testing.T) {
	p := New(
		WithResponses(ContinueResponse("first")),
		WithFallback(StopResponse("fallback", "default stop")),
	)
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "a"})
	if err != nil {
		t.Fatalf("call 1: Execute() error = %v", err)
	}

	result, err := p.Execute(ctx, provider.Request{Prompt: "b"})
	if err != nil {
		t.Fatalf("call 2: Execute() error = %v", err)
	}

	dec := extractDecisionFromStdout(t, result.Stdout)
	if dec["decision"] != "stop" {
		t.Errorf("decision = %q, want %q", dec["decision"], "stop")
	}
}

func TestExecute_ProviderError(t *testing.T) {
	testErr := errors.New("provider crashed")
	p := New(WithResponses(FailureResponse(testErr)))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if !errors.Is(err, testErr) {
		t.Errorf("Execute() error = %v, want %v", err, testErr)
	}
	if result.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", result.ExitCode)
	}
}

func TestExecute_ErrorDecision(t *testing.T) {
	p := New(WithResponses(
		ErrorResponse("failed to compile", "syntax error", []string{"main.go:5: undefined: foo"}),
	))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	dec := extractDecisionFromStdout(t, result.Stdout)
	if dec["decision"] != "error" {
		t.Errorf("decision = %q, want %q", dec["decision"], "error")
	}
}

func TestExecute_NoDecisionEmitted(t *testing.T) {
	p := New(WithResponses(NoDecisionResponse()))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if strings.Contains(result.Stdout, "```ap-result") {
		t.Error("NoDecisionResponse should not emit ap-result block")
	}
}

func TestExecute_StdoutStderr(t *testing.T) {
	p := New(WithResponses(Response{
		Decision: "continue",
		Summary:  "test",
		Stdout:   "hello stdout",
		Stderr:   "hello stderr",
	}))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(result.Stdout, "hello stdout") {
		t.Errorf("Stdout should contain original content")
	}
	if !strings.Contains(result.Stdout, "```ap-result") {
		t.Errorf("Stdout should contain ap-result block")
	}
	if result.Stderr != "hello stderr" {
		t.Errorf("Stderr = %q, want %q", result.Stderr, "hello stderr")
	}
}

func TestExecute_ModelPassthrough(t *testing.T) {
	p := New(WithModel("base-model"))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Model != "base-model" {
		t.Errorf("Model = %q, want %q", result.Model, "base-model")
	}

	result, err = p.Execute(ctx, provider.Request{Prompt: "test", Model: "override-model"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Model != "override-model" {
		t.Errorf("Model = %q, want %q", result.Model, "override-model")
	}
}

func TestExecute_Duration(t *testing.T) {
	p := New(WithResponses(Response{
		Decision: "continue",
		Duration: 500 * time.Millisecond,
	}))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Duration != 500*time.Millisecond {
		t.Errorf("Duration = %v, want %v", result.Duration, 500*time.Millisecond)
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	p := New(WithResponses(Response{
		Decision: "continue",
		Delay:    5 * time.Second,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Execute() error = %v, want context.Canceled", err)
	}
}

func TestExecute_ContextTimeout(t *testing.T) {
	p := New(WithResponses(Response{
		Decision: "continue",
		Delay:    5 * time.Second,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Execute() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestCalls_RecordsRequests(t *testing.T) {
	p := New()
	ctx := context.Background()

	req1 := provider.Request{Prompt: "first", Model: "m1"}
	req2 := provider.Request{Prompt: "second", WorkDir: "/tmp/test"}

	p.Execute(ctx, req1)
	p.Execute(ctx, req2)

	calls := p.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() len = %d, want 2", len(calls))
	}

	if calls[0].Request.Prompt != "first" {
		t.Errorf("call[0].Request.Prompt = %q, want %q", calls[0].Request.Prompt, "first")
	}
	if calls[0].Iteration != 1 {
		t.Errorf("call[0].Iteration = %d, want 1", calls[0].Iteration)
	}
}

func TestReset(t *testing.T) {
	p := New()
	ctx := context.Background()

	p.Execute(ctx, provider.Request{Prompt: "test"})
	if p.CallCount() != 1 {
		t.Fatalf("CallCount() = %d, want 1", p.CallCount())
	}

	p.Reset()
	if p.CallCount() != 0 {
		t.Errorf("CallCount() after Reset = %d, want 0", p.CallCount())
	}
}

func TestExecute_InjectSignal(t *testing.T) {
	p := New(WithResponses(
		InjectResponse("continue", "did work", "focus on error handling"),
	))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	dec := extractDecisionFromStdout(t, result.Stdout)
	signals, ok := dec["signals"].(map[string]any)
	if !ok {
		t.Fatal("signals not present in ap-result block")
	}
	if signals["inject"] != "focus on error handling" {
		t.Errorf("inject = %q, want %q", signals["inject"], "focus on error handling")
	}
}

func TestExecute_EscalateSignal(t *testing.T) {
	p := New(WithResponses(
		EscalateResponse("continue", "need help", "question", "blocked on auth", []string{"opt1", "opt2"}),
	))
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	dec := extractDecisionFromStdout(t, result.Stdout)
	signals, ok := dec["signals"].(map[string]any)
	if !ok {
		t.Fatal("signals not present")
	}
	escalate, ok := signals["escalate"].(map[string]any)
	if !ok {
		t.Fatal("escalate not present")
	}
	if escalate["type"] != "question" {
		t.Errorf("escalate type = %q, want %q", escalate["type"], "question")
	}
}

func TestProviderInterface_Registry(t *testing.T) {
	reg := provider.NewRegistry()
	p := New()

	if err := reg.Register(p); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := reg.Get("mock")
	if !ok {
		t.Fatal("Get(mock) returned false")
	}
	if got.Name() != "mock" {
		t.Errorf("Name() = %q, want %q", got.Name(), "mock")
	}
}

func TestHelperFunctions(t *testing.T) {
	tests := []struct {
		name     string
		response Response
		decision string
	}{
		{"ContinueResponse", ContinueResponse("doing work"), "continue"},
		{"StopResponse", StopResponse("done", "complete"), "stop"},
		{"ErrorResponse", ErrorResponse("broken", "crash", []string{"err1"}), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.response.Decision != tt.decision {
				t.Errorf("Decision = %q, want %q", tt.response.Decision, tt.decision)
			}
		})
	}
}

func TestFailureResponse_Fields(t *testing.T) {
	testErr := errors.New("boom")
	r := FailureResponse(testErr)
	if r.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", r.ExitCode)
	}
	if !errors.Is(r.Err, testErr) {
		t.Errorf("Err = %v, want %v", r.Err, testErr)
	}
}

func TestNoDecisionResponse_Fields(t *testing.T) {
	r := NoDecisionResponse()
	if r.shouldEmitDecision() {
		t.Error("NoDecisionResponse should not emit decision")
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%03d", n)
}

// extractDecisionFromStdout parses the ap-result block JSON from stdout.
func extractDecisionFromStdout(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := strings.Split(stdout, "\n")
	inBlock := false
	var blockContent strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && strings.HasPrefix(trimmed, "```ap-result") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == "```" {
			break
		}
		if inBlock {
			blockContent.WriteString(line)
			blockContent.WriteByte('\n')
		}
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(blockContent.String()), &result); err != nil {
		t.Fatalf("parse ap-result block: %v\nstdout: %s", err, stdout)
	}
	return result
}
