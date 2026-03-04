package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
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

func TestRun_Pipeline_SequentialStages(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("stage 1 iter 1"),
			mock.ContinueResponse("stage 1 iter 2"),
			mock.StopResponse("stage 2 done", "stop stage 2"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "demo-pipeline",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 2},
			{ID: "refine", Stage: "refine-tasks", Runs: 3},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "pipeline-session",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != state.StateCompleted {
		t.Fatalf("status = %q, want %q", res.Status, state.StateCompleted)
	}
	if res.Iterations != 3 {
		t.Fatalf("iterations = %d, want 3", res.Iterations)
	}
	if mp.CallCount() != 3 {
		t.Fatalf("provider calls = %d, want 3", mp.CallCount())
	}

	calls := mp.Calls()
	stageSequence := []string{
		calls[0].Request.Env["AP_STAGE"],
		calls[1].Request.Env["AP_STAGE"],
		calls[2].Request.Env["AP_STAGE"],
	}
	wantStages := []string{"improve-plan", "improve-plan", "refine-tasks"}
	for i := range wantStages {
		if stageSequence[i] != wantStages[i] {
			t.Fatalf("call %d AP_STAGE = %q, want %q", i, stageSequence[i], wantStages[i])
		}
	}

	iterSequence := []string{
		calls[0].Request.Env["AP_ITERATION"],
		calls[1].Request.Env["AP_ITERATION"],
		calls[2].Request.Env["AP_ITERATION"],
	}
	wantIterations := []string{"1", "2", "1"}
	for i := range wantIterations {
		if iterSequence[i] != wantIterations[i] {
			t.Fatalf("call %d AP_ITERATION = %q, want %q", i, iterSequence[i], wantIterations[i])
		}
	}

	st, err := state.Load(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Type != "pipeline" {
		t.Fatalf("state type = %q, want pipeline", st.Type)
	}
	if st.CurrentStage != "refine-tasks" {
		t.Fatalf("current_stage = %q, want refine-tasks", st.CurrentStage)
	}
	if st.NodeID != "refine" {
		t.Fatalf("node_id = %q, want refine", st.NodeID)
	}
	if len(st.Stages) != 2 {
		t.Fatalf("len(stages) = %d, want 2", len(st.Stages))
	}
	if st.Stages[0].CompletedAt == nil || st.Stages[1].CompletedAt == nil {
		t.Fatalf("expected completed_at on both stages: %#v", st.Stages)
	}

	evts := readEvents(t, runDir)
	completed := filterByType(evts, events.TypeSessionComplete)
	if len(completed) != 1 {
		t.Fatalf("session.completed count = %d, want 1", len(completed))
	}
	if got := completed[0].Data["total_iterations"]; got != float64(3) && got != 3 {
		t.Fatalf("session.completed total_iterations = %v, want 3", got)
	}
}

func TestRun_EscalateAlwaysPausesAndRecordsEscalation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		decision string
	}{
		{name: "continue", decision: "continue"},
		{name: "stop", decision: "stop"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runRoot := t.TempDir()
			sessionName := "test-escalate-" + tc.name
			runDir := filepath.Join(runRoot, ".ap", "runs", sessionName)
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				t.Fatalf("mkdir run dir: %v", err)
			}

			mp := mock.New(
				mock.WithResponses(
					mock.EscalateResponse(tc.decision, "need input", "human", "choose A or B", []string{"A", "B"}),
					mock.ContinueResponse("should never run"),
				),
			)

			cfg := Config{
				Session:        sessionName,
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
			if res.Status != state.StatePaused {
				t.Fatalf("status = %q, want %q", res.Status, state.StatePaused)
			}
			if res.Iterations != 1 {
				t.Fatalf("iterations = %d, want 1", res.Iterations)
			}
			if mp.CallCount() != 1 {
				t.Fatalf("provider calls = %d, want 1", mp.CallCount())
			}

			snapshot, err := state.Load(filepath.Join(runDir, "state.json"))
			if err != nil {
				t.Fatalf("load state: %v", err)
			}
			if snapshot.Status != state.StatePaused {
				t.Fatalf("state status = %q, want %q", snapshot.Status, state.StatePaused)
			}
			if snapshot.Escalation == nil {
				t.Fatalf("expected escalation snapshot, got nil")
			}
			if snapshot.Escalation.Type != "human" {
				t.Fatalf("escalation type = %q, want %q", snapshot.Escalation.Type, "human")
			}
			if snapshot.Escalation.Reason != "choose A or B" {
				t.Fatalf("escalation reason = %q, want %q", snapshot.Escalation.Reason, "choose A or B")
			}
			if len(snapshot.Escalation.Options) != 2 || snapshot.Escalation.Options[0] != "A" || snapshot.Escalation.Options[1] != "B" {
				t.Fatalf("escalation options = %#v, want [A B]", snapshot.Escalation.Options)
			}

			eventsPath := filepath.Join(runDir, "events.jsonl")
			data, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatalf("read events: %v", err)
			}

			lines := splitNonEmpty(string(data))
			foundEscalate := false
			for _, line := range lines {
				var evt map[string]any
				if err := json.Unmarshal([]byte(line), &evt); err != nil {
					t.Fatalf("parse event: %v", err)
				}

				switch evt["type"] {
				case events.TypeSessionComplete:
					t.Fatalf("unexpected %q event for escalated run", events.TypeSessionComplete)
				case events.TypeSignalEscalate:
					foundEscalate = true
					payload, ok := evt["data"].(map[string]any)
					if !ok {
						t.Fatalf("signal.escalate data has unexpected type %T", evt["data"])
					}
					if payload["reason"] != "choose A or B" {
						t.Fatalf("event reason = %v, want %q", payload["reason"], "choose A or B")
					}
					options, ok := payload["options"].([]any)
					if !ok {
						t.Fatalf("event options has unexpected type %T", payload["options"])
					}
					if len(options) != 2 || options[0] != "A" || options[1] != "B" {
						t.Fatalf("event options = %#v, want [A B]", options)
					}
				}
			}
			if !foundEscalate {
				t.Fatalf("expected %q event, but none found", events.TypeSignalEscalate)
			}
		})
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
		t.Errorf("events lines = %d, want >= 3 (session.started, iteration.started, iteration.completed, session.completed)", len(lines))
	}

	// Check first event is session.started.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse first event: %v", err)
	}
	if first["type"] != "session.started" {
		t.Errorf("first event type = %q, want session.started", first["type"])
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
	// Expected: session.started, iter1 started/completed, iter2 started/completed, session.completed
	expectedTypes := []string{
		"session.started",
		"iteration.started", "iteration.completed",
		"iteration.started", "iteration.completed",
		"session.completed",
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

func TestRun_InjectSignal_AppearsInNextPrompt(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.InjectResponse("continue", "iteration 1", "focus on tests next"),
			mock.ContinueResponse("iteration 2"),
			mock.ContinueResponse("iteration 3"),
		),
	)

	cfg := Config{
		Session:        "test-inject",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "Do work. Context: ${CONTEXT}",
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}

	calls := mp.Calls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}

	// Iteration 1: no inject yet, ${CONTEXT} should be empty.
	if containsStr(calls[0].Request.Prompt, "focus on tests next") {
		t.Error("iteration 1 prompt should NOT contain injected text")
	}

	// Iteration 2: inject from iteration 1 should be present.
	if !containsStr(calls[1].Request.Prompt, "focus on tests next") {
		t.Errorf("iteration 2 prompt %q should contain injected text", calls[1].Request.Prompt)
	}

	// Iteration 3: inject consumed, should NOT appear again.
	if containsStr(calls[2].Request.Prompt, "focus on tests next") {
		t.Error("iteration 3 prompt should NOT contain injected text (consumed)")
	}
}

func TestRun_InjectSignal_EventEmitted(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.InjectResponse("continue", "did work", "review this change"),
			mock.ContinueResponse("done"),
		),
	)

	cfg := Config{
		Session:        "test-inject-event",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     2,
		PromptTemplate: "iteration ${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	lines := splitNonEmpty(string(data))
	found := false
	for _, line := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		if ev["type"] == "signal.inject" {
			found = true
			evData, ok := ev["data"].(map[string]any)
			if !ok {
				t.Fatal("signal.inject event missing data")
			}
			if evData["iteration"] != float64(1) {
				t.Errorf("event data.iteration = %v, want 1", evData["iteration"])
			}
			break
		}
	}
	if !found {
		t.Error("signal.inject event not found in events.jsonl")
	}
}

func TestRun_InjectSignal_OverwrittenByLaterInject(t *testing.T) {
	runDir := tempSession(t)

	// Both iteration 1 and 2 inject — only iteration 2's inject should appear in iteration 3.
	mp := mock.New(
		mock.WithResponses(
			mock.InjectResponse("continue", "iter 1", "first inject"),
			mock.InjectResponse("continue", "iter 2", "second inject"),
			mock.ContinueResponse("iter 3"),
		),
	)

	cfg := Config{
		Session:        "test-inject-overwrite",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "Context: ${CONTEXT}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()

	// Iteration 2: should have first inject.
	if !containsStr(calls[1].Request.Prompt, "first inject") {
		t.Errorf("iteration 2 prompt %q should contain 'first inject'", calls[1].Request.Prompt)
	}

	// Iteration 3: should have second inject (overwritten).
	if !containsStr(calls[2].Request.Prompt, "second inject") {
		t.Errorf("iteration 3 prompt %q should contain 'second inject'", calls[2].Request.Prompt)
	}
	if containsStr(calls[2].Request.Prompt, "first inject") {
		t.Error("iteration 3 prompt should NOT contain 'first inject'")
	}
}

func TestRun_EscalateSignal_PausesSession(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iteration 1 ok"),
			mock.EscalateResponse("continue", "iteration 2 work", "human", "needs human review", []string{"approve", "reject"}),
		),
	)

	cfg := Config{
		Session:        "test-escalate",
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

	// Escalate should pause after iteration 2, regardless of "continue" decision.
	if res.Status != state.StatePaused {
		t.Errorf("status = %q, want %q", res.Status, state.StatePaused)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if mp.CallCount() != 2 {
		t.Errorf("provider calls = %d, want 2", mp.CallCount())
	}
}

func TestRun_EscalateSignal_OverridesStopDecision(t *testing.T) {
	runDir := tempSession(t)

	// Agent says "stop" but also escalates — escalate wins, session pauses.
	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("stop", "done", "human", "review before finishing", nil),
		),
	)

	cfg := Config{
		Session:        "test-escalate-override",
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

	// Should be paused, not completed (even though agent said "stop").
	if res.Status != state.StatePaused {
		t.Errorf("status = %q, want %q", res.Status, state.StatePaused)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

func TestRun_EscalateSignal_StateHasEscalation(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "work done", "human", "needs approval", []string{"approve", "reject", "defer"}),
		),
	)

	cfg := Config{
		Session:        "test-escalate-state",
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

	// Verify state.json contains escalation info.
	st, err := state.Load(filepath.Join(runDir, "state.json"))
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StatePaused {
		t.Fatalf("state status = %q, want %q", st.Status, state.StatePaused)
	}
	if st.Escalation == nil {
		t.Fatal("state.Escalation is nil, want non-nil")
	}
	if st.Escalation.Type != "human" {
		t.Errorf("escalation type = %q, want %q", st.Escalation.Type, "human")
	}
	if st.Escalation.Reason != "needs approval" {
		t.Errorf("escalation reason = %q, want %q", st.Escalation.Reason, "needs approval")
	}
	if len(st.Escalation.Options) != 3 {
		t.Errorf("escalation options = %v, want 3 options", st.Escalation.Options)
	}
}

func TestRun_EscalateSignal_EventEmitted(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "work", "human", "help needed", []string{"yes", "no"}),
		),
	)

	cfg := Config{
		Session:        "test-escalate-event",
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

	// Verify events.jsonl contains signal.escalate event.
	data, err := os.ReadFile(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	lines := splitNonEmpty(string(data))
	found := false
	for _, line := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		if ev["type"] == "signal.escalate" {
			found = true
			evData, ok := ev["data"].(map[string]any)
			if !ok {
				t.Fatal("signal.escalate event missing data")
			}
			if evData["type"] != "human" {
				t.Errorf("event data.type = %v, want human", evData["type"])
			}
			if evData["reason"] != "help needed" {
				t.Errorf("event data.reason = %v, want 'help needed'", evData["reason"])
			}
			break
		}
	}
	if !found {
		t.Error("signal.escalate event not found in events.jsonl")
	}
}
