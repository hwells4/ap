package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

func tempSession(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	runDir := filepath.Join(dir, ".ap", "runs", "test-session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := mustOpenStore(t)
	return runDir, s
}

func TestRun_FixedIterations_MockProvider(t *testing.T) {
	runDir, s := tempSession(t)

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
			"Output your decision as an ap-result block.",
		Store: s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
	if res.Status != store.StatusCompleted {
		t.Errorf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
	if mp.CallCount() != 3 {
		t.Errorf("provider calls = %d, want 3", mp.CallCount())
	}

	// Verify session state in store is completed.
	row, err := s.GetSession(context.Background(), "test-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusCompleted {
		t.Errorf("state status = %q, want %q", row.Status, store.StatusCompleted)
	}
	if row.IterationCompleted != 3 {
		t.Errorf("iteration_completed = %d, want 3", row.IterationCompleted)
	}
}

func TestRun_EarlyStop(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
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
	runDir, s := tempSession(t)

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
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
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

	row, err := s.GetSession(context.Background(), "pipeline-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Type != "pipeline" {
		t.Fatalf("state type = %q, want pipeline", row.Type)
	}
	if row.CurrentStage != "refine" {
		t.Fatalf("current_stage = %q, want refine", row.CurrentStage)
	}
	if row.NodeID != "refine" {
		t.Fatalf("node_id = %q, want refine", row.NodeID)
	}
	var stages []map[string]any
	if err := json.Unmarshal([]byte(row.StagesJSON), &stages); err != nil {
		t.Fatalf("parse stages_json: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("len(stages) = %d, want 2", len(stages))
	}
	if stages[0]["completed_at"] == nil || stages[1]["completed_at"] == nil {
		t.Fatalf("expected completed_at on both stages: %#v", stages)
	}

	evts := readEvents(t, s, "pipeline-session")
	completed := filterByType(evts, store.TypeSessionComplete)
	if len(completed) != 1 {
		t.Fatalf("session.completed count = %d, want 1", len(completed))
	}
	completedData := parseEventData(t, completed[0])
	if got := completedData["total_iterations"]; got != float64(3) && got != 3 {
		t.Fatalf("session.completed total_iterations = %v, want 3", got)
	}
}

func TestRun_PipelineStageToStageInputs(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("plan iteration 1"),
			mock.StopResponse("plan iteration 2", "stage 1 complete"),
			mock.StopResponse("refine iteration 1", "stage 2 complete"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "stage-inputs",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 2},
			{
				ID:    "refine",
				Stage: "refine-tasks",
				Runs:  1,
				Inputs: compile.Inputs{
					From:   "plan",
					Select: compile.SelectAll,
				},
			},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "pipeline-inputs",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	upstream1 := filepath.Join(runDir, "stage-00-plan", "iterations", "001", "output.md")
	upstream2 := filepath.Join(runDir, "stage-00-plan", "iterations", "002", "output.md")
	for _, path := range []string{upstream1, upstream2} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("upstream output missing %q: %v", path, err)
		}
	}

	ctxPath := filepath.Join(runDir, "stage-01-refine", "iterations", "001", "context.json")
	ctxData, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read downstream context: %v", err)
	}
	var manifest struct {
		Inputs struct {
			FromStage map[string][]string `json:"from_stage"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(ctxData, &manifest); err != nil {
		t.Fatalf("parse downstream context: %v", err)
	}

	got := manifest.Inputs.FromStage["plan"]
	want := []string{upstream1, upstream2}
	if len(got) != len(want) {
		t.Fatalf("from_stage[plan] = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("from_stage[plan][%d] = %q, want %q", i, got[i], want[i])
		}
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

			s := mustOpenStore(t)

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
				Store:          s,
			}

			res, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if res.Status != store.StatusPaused {
				t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
			}
			if res.Iterations != 1 {
				t.Fatalf("iterations = %d, want 1", res.Iterations)
			}
			if mp.CallCount() != 1 {
				t.Fatalf("provider calls = %d, want 1", mp.CallCount())
			}

			row, err := s.GetSession(context.Background(), sessionName)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if row.Status != store.StatusPaused {
				t.Fatalf("state status = %q, want %q", row.Status, store.StatusPaused)
			}
			if row.EscalationJSON == nil {
				t.Fatalf("expected escalation, got nil")
			}
			var escalation map[string]any
			if err := json.Unmarshal([]byte(*row.EscalationJSON), &escalation); err != nil {
				t.Fatalf("parse escalation: %v", err)
			}
			if escalation["type"] != "human" {
				t.Fatalf("escalation type = %v, want human", escalation["type"])
			}
			if escalation["reason"] != "choose A or B" {
				t.Fatalf("escalation reason = %v, want 'choose A or B'", escalation["reason"])
			}
			options, ok := escalation["options"].([]any)
			if !ok || len(options) != 2 || options[0] != "A" || options[1] != "B" {
				t.Fatalf("escalation options = %#v, want [A B]", escalation["options"])
			}

			evts := readEvents(t, s, sessionName)

			foundEscalate := false
			for _, evt := range evts {
				switch evt.Type {
				case store.TypeSessionComplete:
					t.Fatalf("unexpected %q event for escalated run", store.TypeSessionComplete)
				case store.TypeSignalEscalate:
					foundEscalate = true
					payload := parseEventData(t, evt)
					if payload["reason"] != "choose A or B" {
						t.Fatalf("event reason = %v, want %q", payload["reason"], "choose A or B")
					}
					evtOptions, ok := payload["options"].([]any)
					if !ok {
						t.Fatalf("event options has unexpected type %T", payload["options"])
					}
					if len(evtOptions) != 2 || evtOptions[0] != "A" || evtOptions[1] != "B" {
						t.Fatalf("event options = %#v, want [A B]", evtOptions)
					}
				}
			}
			if !foundEscalate {
				t.Fatalf("expected %q event, but none found", store.TypeSignalEscalate)
			}
		})
	}
}

func TestRun_AgentError(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if res.Status != store.StatusCompleted {
		t.Errorf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
}

func TestRun_ProviderFailure(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, store.StatusFailed)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

func TestRun_NoDecisionEmitted(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.NoDecisionResponse()),
	)

	cfg := Config{
		Session:        "test-no-decision",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Errorf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
}

func TestRun_IterationArtifacts(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	ctxPath := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001", "context.json")
	if _, err := os.Stat(ctxPath); err != nil {
		t.Errorf("context.json missing: %v", err)
	}

	outputPath := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001", "output.md")
	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("output.md missing: %v", err)
	}
}

func TestRun_EventsEmitted(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-events")
	if len(evts) < 3 {
		t.Errorf("events count = %d, want >= 3", len(evts))
	}

	if evts[0].Type != "session.started" {
		t.Errorf("first event type = %q, want session.started", evts[0].Type)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, cfg)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_SingleIteration(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if res.Status != store.StatusCompleted {
		t.Errorf("status = %q, want %q", res.Status, store.StatusCompleted)
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
	runDir, s := tempSession(t)

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
		Store:          s,
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
	runDir, s := tempSession(t)

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
		Store:          s,
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
	if !containsStr(prompt, "resolve-test") {
		t.Errorf("prompt %q does not contain session name", prompt)
	}
	if !containsStr(prompt, "1") {
		t.Errorf("prompt %q does not contain iteration number", prompt)
	}
}

func TestRun_RunRequestPersisted(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := t.TempDir()

	mp := mock.New(
		mock.WithResponses(mock.StopResponse("done", "ok")),
	)

	cfg := Config{
		Session:        "persist-test",
		RunDir:         runDir,
		StageName:      "my-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "work on {{task}}",
		Model:          "test-model",
		WorkDir:        workDir,
		OnEscalate:     "webhook:http://localhost/hook",
		Store:          s,
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

	checks := map[string]string{
		"session":         "persist-test",
		"stage":           "my-stage",
		"provider":        "mock",
		"model":           "test-model",
		"work_dir":        workDir,
		"run_dir":         runDir,
		"prompt_template": "work on {{task}}",
		"on_escalate":     "webhook:http://localhost/hook",
	}
	for key, wantVal := range checks {
		got, _ := req[key].(string)
		if got != wantVal {
			t.Errorf("run_request[%q] = %q, want %q", key, got, wantVal)
		}
	}
	if v, ok := req["iterations"].(float64); !ok || int(v) != 5 {
		t.Errorf("iterations = %v, want 5", req["iterations"])
	}
}

func TestRun_RunRequestPersisted_ResumeCompatible(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := t.TempDir()

	mp := mock.New(
		mock.WithResponses(mock.StopResponse("done", "ok")),
	)

	cfg := Config{
		Session:        "resume-compat",
		RunDir:         runDir,
		StageName:      "ralph",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "do work",
		Model:          "opus",
		WorkDir:        workDir,
		Store:          s,
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

	type RunRequestFile struct {
		Session        string `json:"session"`
		Stage          string `json:"stage"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		Iterations     int    `json:"iterations"`
		PromptTemplate string `json:"prompt_template"`
		WorkDir        string `json:"work_dir"`
		RunDir         string `json:"run_dir"`
		OnEscalate     string `json:"on_escalate,omitempty"`
	}

	var req RunRequestFile
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal run_request.json: %v", err)
	}

	if req.Session == "" {
		t.Error("persisted run_request missing session")
	}
	if req.Stage == "" {
		t.Error("persisted run_request missing stage")
	}
	if req.Provider == "" {
		t.Error("persisted run_request missing provider")
	}
	if req.Iterations <= 0 {
		t.Errorf("persisted run_request iterations = %d, must be positive", req.Iterations)
	}
	if req.RunDir == "" {
		t.Error("persisted run_request missing run_dir")
	}
	if req.WorkDir != workDir {
		t.Errorf("work_dir = %q, want %q", req.WorkDir, workDir)
	}
	if req.PromptTemplate != "do work" {
		t.Errorf("prompt_template = %q, want 'do work'", req.PromptTemplate)
	}
}

func TestRun_EventOrdering(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-ordering")
	expectedTypes := []string{
		"session.started",
		"iteration.started", "iteration.completed",
		"iteration.started", "iteration.completed",
		"session.completed",
	}
	if len(evts) != len(expectedTypes) {
		t.Fatalf("event count = %d, want %d", len(evts), len(expectedTypes))
	}
	for i, evt := range evts {
		if evt.Type != expectedTypes[i] {
			t.Errorf("event[%d] type = %q, want %q", i, evt.Type, expectedTypes[i])
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
	runDir, s := tempSession(t)

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
		Store:          s,
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

	if containsStr(calls[0].Request.Prompt, "focus on tests next") {
		t.Error("iteration 1 prompt should NOT contain injected text")
	}

	if !containsStr(calls[1].Request.Prompt, "focus on tests next") {
		t.Errorf("iteration 2 prompt %q should contain injected text", calls[1].Request.Prompt)
	}

	if containsStr(calls[2].Request.Prompt, "focus on tests next") {
		t.Error("iteration 3 prompt should NOT contain injected text (consumed)")
	}
}

func TestRun_InjectSignal_EventEmitted(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-inject-event")
	found := false
	for _, evt := range evts {
		if evt.Type == "signal.inject" {
			found = true
			evData := parseEventData(t, evt)
			if evData["iteration"] != float64(1) {
				t.Errorf("event data.iteration = %v, want 1", evData["iteration"])
			}
			break
		}
	}
	if !found {
		t.Error("signal.inject event not found in store events")
	}
}

func TestRun_InjectSignal_OverwrittenByLaterInject(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()

	if !containsStr(calls[1].Request.Prompt, "first inject") {
		t.Errorf("iteration 2 prompt %q should contain 'first inject'", calls[1].Request.Prompt)
	}

	if !containsStr(calls[2].Request.Prompt, "second inject") {
		t.Errorf("iteration 3 prompt %q should contain 'second inject'", calls[2].Request.Prompt)
	}
	if containsStr(calls[2].Request.Prompt, "first inject") {
		t.Error("iteration 3 prompt should NOT contain 'first inject'")
	}
}

func TestRun_Pipeline_PerStagePromptResolution(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.StopResponse("plan done", "ok"),
			mock.StopResponse("refine done", "ok"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "prompt-resolve-test",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{ID: "refine", Stage: "refine-tasks", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:  "pipeline-prompt-resolve",
		RunDir:   runDir,
		Provider: mp,
		Pipeline: pipeline,
		// PromptTemplate is intentionally empty — runner should resolve per-stage.
		PromptTemplate: "",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
	if res.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", res.Iterations)
	}

	calls := mp.Calls()
	if len(calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(calls))
	}

	// Each stage should have a non-empty prompt (resolved from its stage definition).
	for i, call := range calls {
		if call.Request.Prompt == "" {
			t.Errorf("call[%d] prompt is empty — per-stage prompt resolution failed", i)
		}
	}

	// The two prompts should differ (they come from different stage definitions).
	if calls[0].Request.Prompt == calls[1].Request.Prompt {
		t.Error("both stages got identical prompts — expected different per-stage prompts")
	}
}

func TestRun_Pipeline_StageResolutionFailure(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("should not reach"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "bad-stage-test",
		Nodes: []compile.Node{
			{ID: "bad", Stage: "nonexistent-stage-xyz", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:  "pipeline-bad-stage",
		RunDir:   runDir,
		Provider: mp,
		Pipeline: pipeline,
		WorkDir:  filepath.Dir(filepath.Dir(runDir)),
		Store:    s,
	})
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}
	if res.Status != store.StatusFailed {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusFailed)
	}
	if res.Error == "" {
		t.Fatal("expected non-empty error message for bad stage")
	}
	if mp.CallCount() != 0 {
		t.Fatalf("provider should not be called for unresolvable stage, got %d calls", mp.CallCount())
	}
}

func TestRun_EscalateSignal_PausesSession(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusPaused {
		t.Errorf("status = %q, want %q", res.Status, store.StatusPaused)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if mp.CallCount() != 2 {
		t.Errorf("provider calls = %d, want 2", mp.CallCount())
	}
}

func TestRun_EscalateSignal_OverridesStopDecision(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusPaused {
		t.Errorf("status = %q, want %q", res.Status, store.StatusPaused)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

func TestRun_EscalateSignal_StateHasEscalation(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	row, err := s.GetSession(context.Background(), "test-escalate-state")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusPaused {
		t.Fatalf("state status = %q, want %q", row.Status, store.StatusPaused)
	}
	if row.EscalationJSON == nil {
		t.Fatal("state escalation_json is nil, want non-nil")
	}
	var escalation map[string]any
	if err := json.Unmarshal([]byte(*row.EscalationJSON), &escalation); err != nil {
		t.Fatalf("parse escalation: %v", err)
	}
	if escalation["type"] != "human" {
		t.Errorf("escalation type = %v, want human", escalation["type"])
	}
	if escalation["reason"] != "needs approval" {
		t.Errorf("escalation reason = %v, want 'needs approval'", escalation["reason"])
	}
	opts, ok := escalation["options"].([]any)
	if !ok || len(opts) != 3 {
		t.Errorf("escalation options = %v, want 3 options", escalation["options"])
	}
}

func TestRun_EscalateSignal_EventEmitted(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-escalate-event")
	found := false
	for _, evt := range evts {
		if evt.Type == "signal.escalate" {
			found = true
			evData := parseEventData(t, evt)
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
		t.Error("signal.escalate event not found in store events")
	}
}

func TestRun_Pipeline_RepeatedStageNames(t *testing.T) {
	runDir, s := tempSession(t)

	// 5 iterations total: stage-a:2 -> stage-b:1 -> stage-a:2
	// The second "stage-a" node gets ID "stage-a-2" via uniqueNodeID.
	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("a iter 1"),
			mock.ContinueResponse("a iter 2"),
			mock.ContinueResponse("b iter 1"),
			mock.ContinueResponse("a2 iter 1"),
			mock.ContinueResponse("a2 iter 2"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "repeat-test",
		Nodes: []compile.Node{
			{ID: "stage-a", Stage: "improve-plan", Runs: 2},
			{ID: "stage-b", Stage: "refine-tasks", Runs: 1},
			{ID: "stage-a-2", Stage: "improve-plan", Runs: 2},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "repeat-session",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
	if res.Iterations != 5 {
		t.Fatalf("iterations = %d, want 5", res.Iterations)
	}
	if mp.CallCount() != 5 {
		t.Fatalf("provider calls = %d, want 5", mp.CallCount())
	}

	// All 5 iterations must be tracked in the DB.
	iters, err := s.GetIterations(context.Background(), "repeat-session", "")
	if err != nil {
		t.Fatalf("GetIterations: %v", err)
	}
	if len(iters) != 5 {
		t.Fatalf("DB iterations = %d, want 5", len(iters))
	}

	// Verify stage names in DB use node IDs, not raw stage names.
	wantStages := []string{"stage-a", "stage-a", "stage-b", "stage-a-2", "stage-a-2"}
	for i, iter := range iters {
		if iter.StageName != wantStages[i] {
			t.Errorf("iteration[%d].StageName = %q, want %q", i, iter.StageName, wantStages[i])
		}
	}

	// Verify no error events from store tracking failures.
	evts := readEvents(t, s, "repeat-session")
	for _, evt := range evts {
		if evt.Type == store.TypeError {
			data := parseEventData(t, evt)
			if data["type"] == "store_tracking" {
				t.Errorf("unexpected store_tracking error event: %v", data)
			}
		}
	}
}
