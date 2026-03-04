package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if caps.MaxPromptSize != 10*1024*1024 {
		t.Errorf("MaxPromptSize = %d, want %d", caps.MaxPromptSize, 10*1024*1024)
	}
	if caps.MaxOutputSize != 1*1024*1024 {
		t.Errorf("MaxOutputSize = %d, want %d", caps.MaxOutputSize, 1*1024*1024)
	}
}

func TestExecute_DefaultResponse(t *testing.T) {
	p := New()
	ctx := context.Background()

	result, err := p.Execute(ctx, provider.Request{
		Prompt: "test prompt",
	})
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
}

func TestExecute_PerIterationResponses(t *testing.T) {
	dir := t.TempDir()

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
		statusPath := filepath.Join(dir, "iter", itoa(i+1), "status.json")
		result, err := p.Execute(ctx, provider.Request{
			Prompt:     "test",
			StatusPath: statusPath,
		})
		if err != nil {
			t.Fatalf("iteration %d: Execute() error = %v", i+1, err)
		}
		if result.ExitCode != 0 {
			t.Errorf("iteration %d: ExitCode = %d, want 0", i+1, result.ExitCode)
		}

		// Verify status.json was written.
		data, err := os.ReadFile(statusPath)
		if err != nil {
			t.Fatalf("iteration %d: read status.json error = %v", i+1, err)
		}
		var status map[string]any
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatalf("iteration %d: unmarshal status.json error = %v", i+1, err)
		}
		if status["decision"] != want.decision {
			t.Errorf("iteration %d: decision = %q, want %q", i+1, status["decision"], want.decision)
		}
		if status["summary"] != want.summary {
			t.Errorf("iteration %d: summary = %q, want %q", i+1, status["summary"], want.summary)
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

	// First call uses responses[0].
	_, err := p.Execute(ctx, provider.Request{Prompt: "a"})
	if err != nil {
		t.Fatalf("call 1: Execute() error = %v", err)
	}

	// Second call (beyond responses) uses fallback.
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")
	_, err = p.Execute(ctx, provider.Request{Prompt: "b", StatusPath: statusPath})
	if err != nil {
		t.Fatalf("call 2: Execute() error = %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if status["decision"] != "stop" {
		t.Errorf("decision = %q, want %q", status["decision"], "stop")
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
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")

	p := New(WithResponses(
		ErrorResponse("failed to compile", "syntax error", []string{"main.go:5: undefined: foo"}),
	))
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test", StatusPath: statusPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if status["decision"] != "error" {
		t.Errorf("decision = %q, want %q", status["decision"], "error")
	}
	errs, ok := status["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Errorf("errors = %v, want 1 error", status["errors"])
	}
}

func TestExecute_NoStatusWritten(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")

	p := New(WithResponses(NoStatusResponse()))
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test", StatusPath: statusPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Error("status.json should not exist for NoStatusResponse")
	}
}

func TestExecute_CustomStatusJSON(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")

	customJSON := `{"decision":"continue","custom_field":"custom_value"}`
	p := New(WithResponses(Response{StatusJSON: customJSON}))
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test", StatusPath: statusPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if status["custom_field"] != "custom_value" {
		t.Errorf("custom_field = %v, want %q", status["custom_field"], "custom_value")
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
	if result.Stdout != "hello stdout" {
		t.Errorf("Stdout = %q, want %q", result.Stdout, "hello stdout")
	}
	if result.Stderr != "hello stderr" {
		t.Errorf("Stderr = %q, want %q", result.Stderr, "hello stderr")
	}
	if result.Output != "hello stdout" {
		t.Errorf("Output (legacy) = %q, want %q", result.Output, "hello stdout")
	}
}

func TestExecute_ModelPassthrough(t *testing.T) {
	p := New(WithModel("base-model"))
	ctx := context.Background()

	// Without model in request — uses provider default.
	result, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Model != "base-model" {
		t.Errorf("Model = %q, want %q", result.Model, "base-model")
	}

	// With model in request — uses request model.
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
	if result.FinishedAt.Sub(result.StartedAt) != 500*time.Millisecond {
		t.Errorf("FinishedAt - StartedAt = %v, want %v",
			result.FinishedAt.Sub(result.StartedAt), 500*time.Millisecond)
	}
}

func TestExecute_ContextCancellation(t *testing.T) {
	p := New(WithResponses(Response{
		Decision: "continue",
		Delay:    5 * time.Second,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay.
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
	if calls[1].Request.Prompt != "second" {
		t.Errorf("call[1].Request.Prompt = %q, want %q", calls[1].Request.Prompt, "second")
	}
	if calls[1].Iteration != 2 {
		t.Errorf("call[1].Iteration = %d, want 2", calls[1].Iteration)
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

	// After reset, responses restart from index 0.
	p2 := New(WithResponses(ContinueResponse("first")))
	p2.Execute(ctx, provider.Request{Prompt: "a"})
	p2.Reset()
	p2.Execute(ctx, provider.Request{Prompt: "b"})

	calls := p2.Calls()
	if len(calls) != 1 {
		t.Fatalf("after Reset, Calls() len = %d, want 1", len(calls))
	}
}

func TestExecute_StatusPathEmpty(t *testing.T) {
	// When StatusPath is empty, no status.json should be written anywhere.
	p := New(WithResponses(ContinueResponse("test")))
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// No panic, no error — status is skipped when path is empty.
}

func TestExecute_WorkFields(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "status.json")

	p := New(WithResponses(Response{
		Decision:       "continue",
		Summary:        "did work",
		FilesTouched:   []string{"main.go", "utils.go"},
		ItemsCompleted: []string{"implement feature", "write tests"},
	}))
	ctx := context.Background()

	_, err := p.Execute(ctx, provider.Request{Prompt: "test", StatusPath: statusPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json error = %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}

	work, ok := status["work"].(map[string]any)
	if !ok {
		t.Fatal("work field is not a map")
	}
	files, ok := work["files_touched"].([]any)
	if !ok || len(files) != 2 {
		t.Errorf("files_touched = %v, want 2 items", work["files_touched"])
	}
	items, ok := work["items_completed"].([]any)
	if !ok || len(items) != 2 {
		t.Errorf("items_completed = %v, want 2 items", work["items_completed"])
	}
}

func TestProviderInterface_Registry(t *testing.T) {
	// Verify MockProvider can be registered in the provider registry.
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

func TestNoStatusResponse_Fields(t *testing.T) {
	r := NoStatusResponse()
	if r.shouldWriteStatus() {
		t.Error("NoStatusResponse should not write status")
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%03d", n)
}
