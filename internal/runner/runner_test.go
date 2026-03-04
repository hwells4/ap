package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/result"
	"github.com/hwells4/ap/internal/state"
	"github.com/hwells4/ap/internal/termination"
	"github.com/hwells4/ap/pkg/provider"
)

func tempSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runDir := filepath.Join(dir, ".ap", "runs", "test-session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func TestRun_FixedIterations_MockProvider(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("did iteration 1"),
			mock.ContinueResponse("did iteration 2"),
			mock.ContinueResponse("did iteration 3"),
		),
	)

	three := 3
	cfg := Config{
		Session:    "test-session",
		RunDir:     runDir,
		StageName:  "test-stage",
		Provider:   mp,
		Iterations: three,
		PromptTemplate: "You are iteration ${ITERATION} of ${SESSION_NAME}.\n" +
			"Write status to ${STATUS}.",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
	if res.Status != state.StateCompleted {
		t.Errorf("status = %q, want %q", res.Status, state.StateCompleted)
	}
	if mp.CallCount() != 3 {
		t.Errorf("provider calls = %d, want 3", mp.CallCount())
	}

	// Verify state.json was written and is completed.
	statePath := filepath.Join(runDir, "state.json")
	st, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StateCompleted {
		t.Errorf("state status = %q, want %q", st.Status, state.StateCompleted)
	}
	if st.IterationCompleted != 3 {
		t.Errorf("iteration_completed = %d, want 3", st.IterationCompleted)
	}
}

func TestRun_EarlyStop(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("first"),
			mock.StopResponse("done early", "all work complete"),
		),
	)

	cfg := Config{
		Session:        "test-stop",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     10,
		PromptTemplate: "iteration ${ITERATION}",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if mp.CallCount() != 2 {
		t.Errorf("provider calls = %d, want 2", mp.CallCount())
	}
}

func TestRun_AgentError(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("ok"),
			mock.ErrorResponse("broke", "crash", []string{"bad thing"}),
		),
	)

	cfg := Config{
		Session:        "test-error",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if res.Status != state.StateCompleted {
		t.Errorf("status = %q, want %q", res.Status, state.StateCompleted)
	}
}

func TestRun_ProviderFailure(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.FailureResponse(context.DeadlineExceeded),
		),
	)

	cfg := Config{
		Session:        "test-fail",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION}",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != state.StateFailed {
		t.Errorf("status = %q, want %q", res.Status, state.StateFailed)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

func TestRun_MissingStatusJSON(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.NoStatusResponse()),
	)

	cfg := Config{
		Session:        "test-no-status",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Missing status.json should be treated as iteration failure.
	if res.Status != state.StateFailed {
		t.Errorf("status = %q, want %q", res.Status, state.StateFailed)
	}
}

func TestRun_IterationArtifacts(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iteration 1 work"),
		),
	)

	cfg := Config{
		Session:        "test-artifacts",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Verify context.json exists for iteration 1.
	ctxPath := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001", "context.json")
	if _, err := os.Stat(ctxPath); err != nil {
		t.Errorf("context.json missing: %v", err)
	}

	// Verify status.json exists for iteration 1.
	statusPath := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001", "status.json")
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("status.json missing: %v", err)
	}
}

func TestRun_EventsEmitted(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("work done"),
		),
	)

	cfg := Config{
		Session:        "test-events",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Verify events.jsonl exists and has events.
	eventsPath := filepath.Join(runDir, "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	lines := splitNonEmpty(string(data))
	if len(lines) < 3 {
		t.Errorf("events lines = %d, want >= 3 (session_start, iteration_start, iteration_complete, session_complete)", len(lines))
	}

	// Check first event is session_start.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse first event: %v", err)
	}
	if first["type"] != "session_start" {
		t.Errorf("first event type = %q, want session_start", first["type"])
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "slow work",
			Delay:    5 * time.Second,
		}),
	)

	cfg := Config{
		Session:        "test-cancel",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     100,
		PromptTemplate: "iteration ${ITERATION}",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, cfg)
	// We expect either a context error or a failed result.
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_SingleIteration(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("single"),
		),
	)

	cfg := Config{
		Session:        "test-single",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "do the thing",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if res.Status != state.StateCompleted {
		t.Errorf("status = %q, want %q", res.Status, state.StateCompleted)
	}
}

// splitNonEmpty splits s on newlines and removes blank lines.
func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range splitLines(s) {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestRun_ProviderEnvVars(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.StopResponse("done", "ok")),
	)

	cfg := Config{
		Session:        "test-env",
		RunDir:         runDir,
		StageName:      "my-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "work",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	env := calls[0].Request.Env
	if env["AP_AGENT"] != "1" {
		t.Errorf("AP_AGENT = %q, want %q", env["AP_AGENT"], "1")
	}
	if env["AP_SESSION"] != "test-env" {
		t.Errorf("AP_SESSION = %q, want %q", env["AP_SESSION"], "test-env")
	}
	if env["AP_STAGE"] != "my-stage" {
		t.Errorf("AP_STAGE = %q, want %q", env["AP_STAGE"], "my-stage")
	}
	if env["AP_ITERATION"] != "1" {
		t.Errorf("AP_ITERATION = %q, want %q", env["AP_ITERATION"], "1")
	}
}

func TestRun_PromptResolution(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.StopResponse("done", "ok")),
	)

	cfg := Config{
		Session:        "resolve-test",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "Session=${SESSION_NAME} Iter=${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	prompt := calls[0].Request.Prompt
	if prompt == "Session=${SESSION_NAME} Iter=${ITERATION}" {
		t.Error("prompt template was not resolved")
	}
	// The resolved prompt should contain the session name and iteration number.
	if !containsStr(prompt, "resolve-test") {
		t.Errorf("prompt %q does not contain session name", prompt)
	}
	if !containsStr(prompt, "1") {
		t.Errorf("prompt %q does not contain iteration number", prompt)
	}
}

func TestRun_RunRequestPersisted(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.StopResponse("done", "ok")),
	)

	cfg := Config{
		Session:        "persist-test",
		RunDir:         runDir,
		StageName:      "my-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "work",
		Model:          "test-model",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	reqPath := filepath.Join(runDir, "run_request.json")
	data, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatalf("run_request.json missing: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse run_request.json: %v", err)
	}
	if req["session"] != "persist-test" {
		t.Errorf("session = %v, want persist-test", req["session"])
	}
	if req["stage"] != "my-stage" {
		t.Errorf("stage = %v, want my-stage", req["stage"])
	}
	if v, ok := req["iterations"].(float64); !ok || int(v) != 5 {
		t.Errorf("iterations = %v, want 5", req["iterations"])
	}
}

func TestRun_EventOrdering(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.StopResponse("iter 2", "done"),
		),
	)

	cfg := Config{
		Session:        "test-ordering",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evPath := filepath.Join(runDir, "events.jsonl")
	data, err := os.ReadFile(evPath)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	lines := splitNonEmpty(string(data))
	// Expected: session_start, iter1_start, iter1_complete, iter2_start, iter2_complete, session_complete
	expectedTypes := []string{
		"session_start",
		"iteration_start", "iteration_complete",
		"iteration_start", "iteration_complete",
		"session_complete",
	}
	if len(lines) != len(expectedTypes) {
		t.Fatalf("event count = %d, want %d", len(lines), len(expectedTypes))
	}
	for i, line := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse event %d: %v", i, err)
		}
		if ev["type"] != expectedTypes[i] {
			t.Errorf("event[%d] type = %q, want %q", i, ev["type"], expectedTypes[i])
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// These keep the imports valid even if not all are used in every test.
var _ provider.Provider = (*mock.Provider)(nil)
var _ result.Source = result.SourceStatus
var _ = termination.DefaultFixedIterations
