package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestHooks_SingleStage_FullLifecycle verifies that all 7 hook types fire in
// the correct order during a 3-iteration single-stage run.
func TestHooks_SingleStage_FullLifecycle(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir))) // project root

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.ContinueResponse("iter 2"),
			mock.ContinueResponse("iter 3"),
		),
	)

	// Use a log file that records hook execution order.
	logFile := filepath.Join(workDir, "hook-order.log")

	cfg := Config{
		Session:        "hooks-lifecycle",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PreSession:    "echo pre_session >> " + logFile,
			PreIteration:  "echo pre_iteration >> " + logFile,
			PostIteration: "echo post_iteration >> " + logFile,
			PostSession:   "echo post_session >> " + logFile,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
	if res.Iterations != 3 {
		t.Fatalf("iterations = %d, want 3", res.Iterations)
	}

	// Read hook order log.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read hook log: %v", err)
	}
	lines := nonEmptyLines(string(data))

	// Expected: pre_session, (pre_iteration, post_iteration) × 3, post_session
	expected := []string{
		"pre_session",
		"pre_iteration", "post_iteration",
		"pre_iteration", "post_iteration",
		"pre_iteration", "post_iteration",
		"post_session",
	}
	if len(lines) != len(expected) {
		t.Fatalf("hook log lines = %d, want %d; got: %v", len(lines), len(expected), lines)
	}
	for i, want := range expected {
		if lines[i] != want {
			t.Errorf("hook log[%d] = %q, want %q", i, lines[i], want)
		}
	}

	// Verify hook.completed events.
	evts := readEvents(t, s, "hooks-lifecycle")
	hookCompleted := filterByType(evts, store.TypeHookCompleted)
	// 1 pre_session + 3 pre_iteration + 3 post_iteration + 1 post_session = 8
	if len(hookCompleted) != 8 {
		t.Fatalf("hook.completed events = %d, want 8", len(hookCompleted))
	}
}

// TestHooks_SingleStage_VariableValues verifies that hook variables resolve
// correctly across iterations.
func TestHooks_SingleStage_VariableValues(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.ContinueResponse("iter 2"),
		),
	)

	logFile := filepath.Join(workDir, "vars.log")

	cfg := Config{
		Session:        "hooks-vars",
		RunDir:         runDir,
		StageName:      "my-stage",
		Provider:       mp,
		Iterations:     2,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PostIteration: "echo ${SESSION}:${STAGE}:${ITERATION}:${STATUS} >> " + logFile,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read vars log: %v", err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2; got: %v", len(lines), lines)
	}
	if lines[0] != "hooks-vars:my-stage:1:running" {
		t.Errorf("line 0 = %q, want hooks-vars:my-stage:1:running", lines[0])
	}
	if lines[1] != "hooks-vars:my-stage:2:running" {
		t.Errorf("line 1 = %q, want hooks-vars:my-stage:2:running", lines[1])
	}
}

// TestHooks_SingleStage_SummaryVariable verifies that ${SUMMARY} contains the
// agent's iteration summary from the ap-result block.
func TestHooks_SingleStage_SummaryVariable(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("added-login-page"),
			mock.ContinueResponse("fixed-auth-bug"),
		),
	)

	logFile := filepath.Join(workDir, "summary.log")

	cfg := Config{
		Session:        "hooks-summary",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     2,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PostIteration: "echo $AP_SUMMARY >> " + logFile,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read summary log: %v", err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2; got: %v", len(lines), lines)
	}
	if lines[0] != "added-login-page" {
		t.Errorf("line 0 = %q, want added-login-page", lines[0])
	}
	if lines[1] != "fixed-auth-bug" {
		t.Errorf("line 1 = %q, want fixed-auth-bug", lines[1])
	}
}

// TestHooks_SingleStage_FailureHook verifies that on_failure fires on provider
// failure and post_session does NOT fire.
func TestHooks_SingleStage_FailureHook(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("ok"),
			mock.FailureResponse(context.DeadlineExceeded),
		),
	)

	failureMarker := filepath.Join(workDir, "on-failure.marker")
	successMarker := filepath.Join(workDir, "post-session.marker")

	cfg := Config{
		Session:        "hooks-failure",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			OnFailure:   "touch " + failureMarker,
			PostSession: "touch " + successMarker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusFailed {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusFailed)
	}

	// on_failure should have fired.
	if _, err := os.Stat(failureMarker); os.IsNotExist(err) {
		t.Error("on_failure marker missing — hook did not fire")
	}

	// post_session should NOT have fired.
	if _, err := os.Stat(successMarker); err == nil {
		t.Error("post_session marker exists — hook should not fire on failure")
	}
}

// TestHooks_SingleStage_HookFailureNonFatal verifies that a failing hook
// does not abort the session.
func TestHooks_SingleStage_HookFailureNonFatal(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.ContinueResponse("iter 2"),
		),
	)

	cfg := Config{
		Session:        "hooks-nonfatal",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     2,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PreIteration: "exit 1", // always fails
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q — hook failure should be non-fatal", res.Status, store.StatusCompleted)
	}
	if res.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", res.Iterations)
	}

	// Verify hook.failed events emitted.
	evts := readEvents(t, s, "hooks-nonfatal")
	hookFailed := filterByType(evts, store.TypeHookFailed)
	if len(hookFailed) != 2 {
		t.Fatalf("hook.failed events = %d, want 2", len(hookFailed))
	}
	for _, evt := range hookFailed {
		data := parseEventData(t, evt)
		if data["hook"] != "pre_iteration" {
			t.Errorf("hook.failed hook = %v, want pre_iteration", data["hook"])
		}
	}
}

// TestHooks_Pipeline_PreSessionAndPostSession verifies that pre_session and
// post_session hooks fire around a full pipeline run.
func TestHooks_Pipeline_PreSessionAndPostSession(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "pipeline work",
		}),
	)

	preMarker := filepath.Join(workDir, "pre-session.marker")
	postMarker := filepath.Join(workDir, "post-session.marker")

	pipeline := &compile.Pipeline{
		Name: "hooks-pipeline",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{ID: "implement", Stage: "improve-plan", Runs: 1},
		},
	}

	cfg := Config{
		Session:        "hooks-pipeline",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PreSession:  "touch " + preMarker,
			PostSession: "touch " + postMarker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	if _, err := os.Stat(preMarker); os.IsNotExist(err) {
		t.Error("pre_session marker missing")
	}
	if _, err := os.Stat(postMarker); os.IsNotExist(err) {
		t.Error("post_session marker missing")
	}

	// Verify hook.completed events.
	evts := readEvents(t, s, "hooks-pipeline")
	hookCompleted := filterByType(evts, store.TypeHookCompleted)
	foundPre := false
	foundPost := false
	for _, evt := range hookCompleted {
		data := parseEventData(t, evt)
		if data["hook"] == "pre_session" {
			foundPre = true
		}
		if data["hook"] == "post_session" {
			foundPost = true
		}
	}
	if !foundPre {
		t.Error("no hook.completed event for pre_session")
	}
	if !foundPost {
		t.Error("no hook.completed event for post_session")
	}
}

// TestHooks_Pipeline_PostStageFiresBetweenNodes verifies that post_stage fires
// after each pipeline node, recording stage names in order.
func TestHooks_Pipeline_PostStageFiresBetweenNodes(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "stage work",
		}),
	)

	logFile := filepath.Join(workDir, "post-stage.log")

	pipeline := &compile.Pipeline{
		Name: "stage-hooks",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{ID: "refine", Stage: "refine-tasks", Runs: 1},
			{ID: "implement", Stage: "improve-plan", Runs: 1},
		},
	}

	cfg := Config{
		Session:        "hooks-post-stage",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PostStage: "echo $AP_STAGE >> " + logFile,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read post-stage log: %v", err)
	}
	lines := nonEmptyLines(string(data))
	if len(lines) != 3 {
		t.Fatalf("post_stage lines = %d, want 3; got: %v", len(lines), lines)
	}
	// Stage names are the stage (not node ID) in the hook context.
	wantStages := []string{"improve-plan", "refine-tasks", "improve-plan"}
	for i, want := range wantStages {
		if lines[i] != want {
			t.Errorf("post_stage[%d] = %q, want %q", i, lines[i], want)
		}
	}
}

// TestHooks_Pipeline_PreIterationFiresBeforeProvider is a smoke test verifying
// that pre_iteration hooks fire within a pipeline stage execution.
func TestHooks_Pipeline_PreIterationFiresBeforeProvider(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "work",
		}),
	)

	marker := filepath.Join(workDir, "pre-iter-pipeline.marker")

	pipeline := &compile.Pipeline{
		Name: "pre-iter-pipeline",
		Nodes: []compile.Node{
			{ID: "step", Stage: "improve-plan", Runs: 1},
		},
	}

	cfg := Config{
		Session:        "hooks-pre-iter-pipeline",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PreIteration: "touch " + marker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("pre_iteration marker missing — hook did not fire in pipeline")
	}
}

// nonEmptyLines splits text on newlines and returns non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
