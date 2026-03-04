package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

// TestDispatchState_EscalateInFlight verifies that a dispatching event for
// an escalate signal without a corresponding result is reported as in-flight.
func TestDispatchState_EscalateInFlight(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-escalate-0",
		"signal_type": "escalate",
		"iteration":   1,
	})

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	if !ds.IsInFlight("sig-1-escalate-0") {
		t.Error("escalate signal should be in-flight after crash")
	}
	if ds.IsCompleted("sig-1-escalate-0") {
		t.Error("escalate signal should NOT be completed after crash")
	}
	if ds.ShouldSkip("sig-1-escalate-0") {
		t.Error("in-flight escalate should NOT be skipped")
	}
}

// TestDispatchState_DuplicateDispatching verifies that the same signal_id
// appearing in two dispatching events (e.g., crash-and-replay) is tracked
// as a single dispatch, not creating duplicates.
func TestDispatchState_DuplicateDispatching(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	// Simulate a crash-replay: dispatching emitted twice for the same ID.
	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	// Only one result event.
	writeEvent(t, evPath, events.TypeSignalSpawn, "test", map[string]any{
		"signal_id":     "sig-1-spawn-0",
		"child_session": "child-1",
	})

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	if !ds.IsCompleted("sig-1-spawn-0") {
		t.Error("duplicate dispatching + result should still be completed")
	}
	if ds.IsInFlight("sig-1-spawn-0") {
		t.Error("should not be in-flight after result event")
	}
}

// TestDispatchState_SignalIDIsolation verifies that different signal IDs
// (different types, iterations, indices) are tracked independently.
func TestDispatchState_SignalIDIsolation(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	// spawn signal completed.
	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeEvent(t, evPath, events.TypeSignalSpawn, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-1",
	})

	// escalate signal in-flight.
	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-escalate-0", "signal_type": "escalate", "iteration": 1,
	})

	// spawn signal from iteration 2, not yet dispatched.

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("completed spawn should be skipped")
	}
	if ds.ShouldSkip("sig-1-escalate-0") {
		t.Error("in-flight escalate should NOT be skipped")
	}
	if ds.IsInFlight("sig-2-spawn-0") {
		t.Error("undispatched signal should not be in-flight")
	}
	if ds.ShouldSkip("sig-2-spawn-0") {
		t.Error("undispatched signal should not be skipped")
	}
}

// TestIntegration_CrashDuringEscalate_Recovery verifies the full escalate
// crash-recovery scenario: dispatching emitted, then crash (no escalate
// result), then recovery adds the result.
func TestIntegration_CrashDuringEscalate_Recovery(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	// Simulate normal lifecycle events before the crash.
	writeEvent(t, evPath, events.TypeSessionStart, "esc-crash", map[string]any{
		"stage": "test", "iterations": 5,
	})
	writeEvent(t, evPath, events.TypeIterationStart, "esc-crash", map[string]any{
		"iteration": 1,
	})
	writeEvent(t, evPath, events.TypeIterationComplete, "esc-crash", map[string]any{
		"iteration": 1, "decision": "continue",
	})
	// Escalate dispatching emitted, then crash.
	writeEvent(t, evPath, events.TypeSignalDispatching, "esc-crash", map[string]any{
		"signal_id":   "sig-1-escalate-0",
		"signal_type": "escalate",
		"iteration":   1,
	})

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	if !ds.IsInFlight("sig-1-escalate-0") {
		t.Error("escalate should be in-flight after crash")
	}

	// Simulate recovery: escalate result event.
	writeEvent(t, evPath, events.TypeSignalEscalate, "esc-crash", map[string]any{
		"signal_id": "sig-1-escalate-0",
		"iteration": 1,
		"type":      "human",
		"reason":    "review needed",
	})

	ds2, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState after recovery: %v", err)
	}

	if !ds2.IsCompleted("sig-1-escalate-0") {
		t.Error("escalate should be completed after recovery")
	}
	if ds2.IsInFlight("sig-1-escalate-0") {
		t.Error("escalate should NOT be in-flight after recovery")
	}
	if !ds2.ShouldSkip("sig-1-escalate-0") {
		t.Error("completed escalate should be skipped on replay")
	}
}

// TestIntegration_FullReplayScenario builds a comprehensive events.jsonl with
// multiple signal types in different states and verifies DispatchState
// correctly identifies each signal's lifecycle phase.
func TestIntegration_FullReplayScenario(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	// Iteration 1: two spawns — first completed, second failed.
	writeEvent(t, evPath, events.TypeSessionStart, "replay", map[string]any{
		"stage": "test", "iterations": 10,
	})
	writeEvent(t, evPath, events.TypeIterationStart, "replay", map[string]any{"iteration": 1})
	writeEvent(t, evPath, events.TypeIterationComplete, "replay", map[string]any{
		"iteration": 1, "decision": "continue",
	})
	writeEvent(t, evPath, events.TypeSignalDispatching, "replay", map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeEvent(t, evPath, events.TypeSignalSpawn, "replay", map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-a",
	})
	writeEvent(t, evPath, events.TypeSignalDispatching, "replay", map[string]any{
		"signal_id": "sig-1-spawn-1", "signal_type": "spawn", "iteration": 1,
	})
	writeEvent(t, evPath, events.TypeSignalSpawnFailed, "replay", map[string]any{
		"signal_id": "sig-1-spawn-1", "error": "stage not found",
	})

	// Iteration 2: inject (no dispatch state), then spawn in-flight (crash).
	writeEvent(t, evPath, events.TypeIterationStart, "replay", map[string]any{"iteration": 2})
	writeEvent(t, evPath, events.TypeIterationComplete, "replay", map[string]any{
		"iteration": 2, "decision": "continue",
	})
	writeEvent(t, evPath, events.TypeSignalInject, "replay", map[string]any{
		"iteration": 2, "length": 42,
	})
	writeEvent(t, evPath, events.TypeSignalDispatching, "replay", map[string]any{
		"signal_id": "sig-2-spawn-0", "signal_type": "spawn", "iteration": 2,
	})
	// No result event for sig-2-spawn-0 — this is in-flight (crash).

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	tests := []struct {
		id         string
		completed  bool
		inFlight   bool
		shouldSkip bool
	}{
		{"sig-1-spawn-0", true, false, true},   // completed
		{"sig-1-spawn-1", true, false, true},    // failed (also completed)
		{"sig-2-spawn-0", false, true, false},   // in-flight (needs re-dispatch)
		{"sig-3-spawn-0", false, false, false},  // never dispatched
	}

	for _, tt := range tests {
		if ds.IsCompleted(tt.id) != tt.completed {
			t.Errorf("%s: IsCompleted = %v, want %v", tt.id, ds.IsCompleted(tt.id), tt.completed)
		}
		if ds.IsInFlight(tt.id) != tt.inFlight {
			t.Errorf("%s: IsInFlight = %v, want %v", tt.id, ds.IsInFlight(tt.id), tt.inFlight)
		}
		if ds.ShouldSkip(tt.id) != tt.shouldSkip {
			t.Errorf("%s: ShouldSkip = %v, want %v", tt.id, ds.ShouldSkip(tt.id), tt.shouldSkip)
		}
	}
}

// TestRun_SpawnLimitsCumulativeAcrossIterations verifies that the spawn child
// count accumulates across iterations, not per-iteration.
func TestRun_SpawnLimitsCumulativeAcrossIterations(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-cumulative")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	// Iteration 1 spawns one child, iteration 2 attempts another.
	prov := mock.New(
		mock.WithResponses(
			mock.Response{
				StatusJSON: `{
					"decision":"continue",
					"reason":"next",
					"summary":"spawn first",
					"work":{"items_completed":[],"files_touched":[]},
					"errors":[],
					"agent_signals":{"spawn":[{"run":"ralph","session":"child-1"}]}
				}`,
			},
			mock.Response{
				StatusJSON: `{
					"decision":"stop",
					"reason":"done",
					"summary":"spawn second",
					"work":{"items_completed":[],"files_touched":[]},
					"errors":[],
					"agent_signals":{"spawn":[{"run":"ralph","session":"child-2"}]}
				}`,
			},
		),
	)

	launcher := &spawnTestLauncher{}
	_, err := Run(context.Background(), Config{
		Session:          "parent-cumulative",
		RunDir:           runDir,
		StageName:        "ralph",
		Provider:         prov,
		Iterations:       2,
		PromptTemplate:   "iteration ${ITERATION}",
		WorkDir:          root,
		Launcher:         launcher,
		ExecutablePath:   "/usr/bin/ap",
		SpawnMaxChildren: 1, // only 1 child allowed total
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Launcher should have been called exactly once (first spawn succeeds).
	if launcher.startCalls != 1 {
		t.Fatalf("launcher start calls = %d, want 1", launcher.startCalls)
	}

	evts := readEvents(t, runDir)
	spawned := filterByType(evts, "signal.spawn")
	failed := filterByType(evts, "signal.spawn.failed")
	if len(spawned) != 1 {
		t.Fatalf("signal.spawn count = %d, want 1", len(spawned))
	}
	if len(failed) != 1 {
		t.Fatalf("signal.spawn.failed count = %d, want 1 (cumulative limit)", len(failed))
	}
}

// TestRun_EscalateSignal_AlwaysPauses verifies that an escalate signal
// overrides any agent decision and pauses the session.
func TestRun_EscalateSignal_AlwaysPauses(t *testing.T) {
	runDir := tempSession(t)

	// Agent says "continue" but also sends escalate — escalate wins.
	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "keep going", "human", "need approval", []string{"yes", "no"}),
		),
	)

	res, err := Run(context.Background(), Config{
		Session:        "test-escalate-pause",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     10,
		PromptTemplate: "iteration ${ITERATION}",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != state.StatePaused {
		t.Errorf("status = %v, want paused", res.Status)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1 (should stop after escalation)", res.Iterations)
	}

	// Verify state.json shows paused with escalation info.
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StatePaused {
		t.Errorf("state status = %v, want paused", st.Status)
	}
	if st.Escalation == nil {
		t.Fatal("state missing escalation info")
	}
	if st.Escalation.Type != "human" {
		t.Errorf("escalation type = %q, want human", st.Escalation.Type)
	}
}

// TestRun_InjectSignal_ConsumedOnce verifies that inject signal text is
// applied to the next iteration's prompt and then cleared.
func TestRun_InjectSignal_ConsumedOnce(t *testing.T) {
	runDir := tempSession(t)

	// Iteration 1 injects context, iteration 2 and 3 do not inject.
	mp := mock.New(
		mock.WithResponses(
			mock.InjectResponse("continue", "did work", "INJECTED CONTEXT HERE"),
			mock.ContinueResponse("used injected context"),
			mock.StopResponse("done", "finished"),
		),
	)

	res, err := Run(context.Background(), Config{
		Session:        "test-inject-once",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION} context=${CONTEXT}",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != state.StateCompleted {
		t.Errorf("status = %v, want completed", res.Status)
	}

	evts := readEvents(t, runDir)
	injects := filterByType(evts, events.TypeSignalInject)
	if len(injects) != 1 {
		t.Fatalf("signal.inject events = %d, want 1", len(injects))
	}

	// Verify the inject event has the right iteration.
	if injects[0].Data["iteration"] != float64(1) {
		t.Errorf("inject iteration = %v, want 1", injects[0].Data["iteration"])
	}

	// Verify the provider received the injected context in iteration 2
	// but not in iteration 3. MockProvider records Calls with Request.Prompt.
	calls := mp.Calls()
	if len(calls) < 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	// Iteration 2 prompt should contain the injected text.
	if !containsSubstr(calls[1].Request.Prompt, "INJECTED CONTEXT HERE") {
		t.Errorf("iteration 2 prompt should contain injected context, got: %s", calls[1].Request.Prompt[:min(100, len(calls[1].Request.Prompt))])
	}
	// Iteration 3 prompt should NOT contain the injected text (consumed).
	if containsSubstr(calls[2].Request.Prompt, "INJECTED CONTEXT HERE") {
		t.Errorf("iteration 3 prompt should NOT contain injected context (consumed)")
	}
}

// TestDispatchState_HandlerErrorDoesNotAffectCompletion verifies that
// signal.handler.error events don't interfere with dispatch state tracking.
func TestDispatchState_HandlerErrorDoesNotAffectCompletion(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "events.jsonl")

	writeEvent(t, evPath, events.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	// Handler error occurs but spawn still succeeds.
	writeEvent(t, evPath, events.TypeSignalHandlerError, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "error": "webhook timeout",
	})
	writeEvent(t, evPath, events.TypeSignalSpawn, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-1",
	})

	ds, err := LoadDispatchState(evPath)
	if err != nil {
		t.Fatalf("LoadDispatchState: %v", err)
	}

	if !ds.IsCompleted("sig-1-spawn-0") {
		t.Error("signal should be completed despite handler error")
	}
	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("completed signal should be skipped on replay")
	}
}

// TestRun_ChildSessionTrackedInState verifies that spawned child sessions
// are recorded in the parent's state.json.
func TestRun_ChildSessionTrackedInState(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-track")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(mock.Response{
			StatusJSON: `{
				"decision":"stop",
				"reason":"done",
				"summary":"spawn child",
				"work":{"items_completed":[],"files_touched":[]},
				"errors":[],
				"agent_signals":{"spawn":[{"run":"ralph","session":"child-tracked"}]}
			}`,
		}),
	)

	launcher := &spawnTestLauncher{}
	_, err := Run(context.Background(), Config{
		Session:        "parent-track",
		RunDir:         runDir,
		StageName:      "ralph",
		Provider:       prov,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        root,
		Launcher:       launcher,
		ExecutablePath: "/usr/bin/ap",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if len(st.ChildSessions) != 1 {
		t.Fatalf("child_sessions = %v, want 1 entry", st.ChildSessions)
	}
	if st.ChildSessions[0] != "child-tracked" {
		t.Errorf("child_sessions[0] = %q, want child-tracked", st.ChildSessions[0])
	}
}

