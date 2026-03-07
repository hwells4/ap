package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestSignal_InjectCrossStage_Pipeline verifies that inject signals are
// stage-scoped: they carry to the next iteration within the same stage,
// but do NOT leak across stage boundaries.
func TestSignal_InjectCrossStage_Pipeline(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			// stage-a iter 1: inject "focus-on-tests"
			mock.InjectResponse("continue", "a iter 1", "focus-on-tests"),
			// stage-a iter 2: should see "focus-on-tests" in prompt
			mock.ContinueResponse("a iter 2"),
			// stage-b iter 1: should NOT see "focus-on-tests"
			mock.ContinueResponse("b iter 1"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "inject-cross-stage",
		Nodes: []compile.Node{
			{ID: "stage-a", Stage: "improve-plan", Runs: 2},
			{ID: "stage-b", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "inject-cross-stage",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "Context: ${CONTEXT}",
		WorkDir:        filepath.Dir(filepath.Dir(filepath.Dir(runDir))),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	calls := mp.Calls()
	if len(calls) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(calls))
	}

	// Stage-a iteration 2 should contain the inject.
	if !containsStr(calls[1].Request.Prompt, "focus-on-tests") {
		t.Errorf("stage-a iter 2 prompt should contain 'focus-on-tests', got: %q", calls[1].Request.Prompt)
	}

	// Stage-b iteration 1 should NOT contain the inject (consumed in stage-a).
	if containsStr(calls[2].Request.Prompt, "focus-on-tests") {
		t.Errorf("stage-b iter 1 prompt should NOT contain 'focus-on-tests', got: %q", calls[2].Request.Prompt)
	}

	// Verify signal.inject event was emitted.
	evts := readEvents(t, s, "inject-cross-stage")
	injectEvts := filterByType(evts, store.TypeSignalInject)
	if len(injectEvts) != 1 {
		t.Fatalf("signal.inject events = %d, want 1", len(injectEvts))
	}
}

// TestSignal_Escalate_PausesAndNoPostSession verifies that an escalate signal
// pauses the session and does NOT fire post_session.
func TestSignal_Escalate_PausesAndNoPostSession(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need review", "human", "approve changes", []string{"yes", "no"}),
		),
	)

	postSessionMarker := filepath.Join(workDir, "post-session-escalate.marker")

	cfg := Config{
		Session:        "escalate-no-post",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PostSession: "touch " + postSessionMarker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}

	// signal.escalate event should exist.
	evts := readEvents(t, s, "escalate-no-post")
	escalateEvts := filterByType(evts, store.TypeSignalEscalate)
	if len(escalateEvts) != 1 {
		t.Fatalf("signal.escalate events = %d, want 1", len(escalateEvts))
	}

	// post_session should NOT have fired.
	if _, err := os.Stat(postSessionMarker); err == nil {
		t.Error("post_session marker exists — should not fire when session is paused via escalation")
	}

	// session.completed should NOT exist (session is paused, not completed).
	sessionComplete := filterByType(evts, store.TypeSessionComplete)
	if len(sessionComplete) != 0 {
		t.Errorf("session.completed events = %d, want 0 (session paused)", len(sessionComplete))
	}
}

// TestSignal_Escalate_Pipeline_PausesEntirePipeline verifies that an escalate
// signal during a pipeline node pauses the entire pipeline. Node 2 never runs.
func TestSignal_Escalate_Pipeline_PausesEntirePipeline(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			// Node 1 iteration 1: escalate.
			mock.EscalateResponse("continue", "need approval", "human", "design review", nil),
			// Node 2 should never run.
			mock.ContinueResponse("should not run"),
		),
	)

	pipeline := &compile.Pipeline{
		Name: "escalate-pipeline",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{ID: "implement", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "escalate-pipeline",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(filepath.Dir(runDir))),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}
	if res.Iterations != 1 {
		t.Fatalf("iterations = %d, want 1 — only first node iteration should run", res.Iterations)
	}
	if mp.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 — node 2 should not execute", mp.CallCount())
	}
}

// TestSignal_InjectOverwritten_SameStage verifies that consecutive inject
// signals overwrite each other: iteration N+1 sees the inject from iteration N,
// and iteration N+2 sees the inject from iteration N+1.
func TestSignal_InjectOverwritten_SameStage(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.InjectResponse("continue", "iter 1", "first-inject"),
			mock.InjectResponse("continue", "iter 2", "second-inject"),
			mock.ContinueResponse("iter 3"),
		),
	)

	cfg := Config{
		Session:        "inject-overwrite",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "Context: ${CONTEXT}",
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	calls := mp.Calls()
	if len(calls) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(calls))
	}

	// Iteration 2 should see "first-inject" (from iteration 1).
	if !containsStr(calls[1].Request.Prompt, "first-inject") {
		t.Errorf("iter 2 prompt should contain 'first-inject', got: %q", calls[1].Request.Prompt)
	}

	// Iteration 3 should see "second-inject" (from iteration 2), NOT "first-inject".
	if !containsStr(calls[2].Request.Prompt, "second-inject") {
		t.Errorf("iter 3 prompt should contain 'second-inject', got: %q", calls[2].Request.Prompt)
	}
	if containsStr(calls[2].Request.Prompt, "first-inject") {
		t.Errorf("iter 3 prompt should NOT contain 'first-inject', got: %q", calls[2].Request.Prompt)
	}
}

// TestSignal_Escalate_OnFailureDoesNotFire verifies that an escalation does
// NOT trigger the on_failure hook — escalation is not a failure.
func TestSignal_Escalate_OnFailureDoesNotFire(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "escalating", "human", "need approval", nil),
		),
	)

	failureMarker := filepath.Join(workDir, "on-failure-escalate.marker")

	cfg := Config{
		Session:        "escalate-no-failure",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
		WorkDir:        workDir,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			OnFailure: "touch " + failureMarker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}

	// on_failure should NOT fire on escalation.
	if _, err := os.Stat(failureMarker); err == nil {
		t.Error("on_failure marker exists — escalation should NOT trigger on_failure hook")
	}

	// No hook.completed event for on_failure.
	evts := readEvents(t, s, "escalate-no-failure")
	hookCompleted := filterByType(evts, store.TypeHookCompleted)
	for _, evt := range hookCompleted {
		data := parseEventData(t, evt)
		if data["hook"] == "on_failure" {
			t.Error("hook.completed event for on_failure found — should not exist on escalation")
		}
	}
}
