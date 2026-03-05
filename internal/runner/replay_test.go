package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestDispatchState_EscalateInFlight verifies that a dispatching event for
// an escalate signal without a corresponding result is reported as in-flight.
func TestDispatchState_EscalateInFlight(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "test", "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-escalate-0",
		"signal_type": "escalate",
		"iteration":   1,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, "test")
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
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
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "test", "stage", "test", "{}")

	// Simulate a crash-replay: dispatching emitted twice for the same ID.
	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	// Only one result event.
	writeStoreEvent(t, s, store.TypeSignalSpawn, "test", map[string]any{
		"signal_id":     "sig-1-spawn-0",
		"child_session": "child-1",
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, "test")
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
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
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "test", "stage", "test", "{}")

	// spawn signal completed.
	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawn, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-1",
	})

	// escalate signal in-flight.
	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-escalate-0", "signal_type": "escalate", "iteration": 1,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, "test")
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
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
// crash-recovery scenario.
func TestIntegration_CrashDuringEscalate_Recovery(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "esc-crash"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSessionStart, session, map[string]any{
		"stage": "test", "iterations": 5,
	})
	writeStoreEvent(t, s, store.TypeIterationStart, session, map[string]any{
		"iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeIterationComplete, session, map[string]any{
		"iteration": 1, "decision": "continue",
	})
	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id":   "sig-1-escalate-0",
		"signal_type": "escalate",
		"iteration":   1,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsInFlight("sig-1-escalate-0") {
		t.Error("escalate should be in-flight after crash")
	}

	writeStoreEvent(t, s, store.TypeSignalEscalate, session, map[string]any{
		"signal_id": "sig-1-escalate-0",
		"iteration": 1,
		"type":      "human",
		"reason":    "review needed",
	})

	ds2, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore after recovery: %v", err)
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

// TestIntegration_FullReplayScenario builds a comprehensive event log with
// multiple signal types in different states.
func TestIntegration_FullReplayScenario(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "replay"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSessionStart, session, map[string]any{
		"stage": "test", "iterations": 10,
	})
	writeStoreEvent(t, s, store.TypeIterationStart, session, map[string]any{"iteration": 1})
	writeStoreEvent(t, s, store.TypeIterationComplete, session, map[string]any{
		"iteration": 1, "decision": "continue",
	})
	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawn, session, map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-a",
	})
	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-1-spawn-1", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawnFailed, session, map[string]any{
		"signal_id": "sig-1-spawn-1", "error": "stage not found",
	})

	writeStoreEvent(t, s, store.TypeIterationStart, session, map[string]any{"iteration": 2})
	writeStoreEvent(t, s, store.TypeIterationComplete, session, map[string]any{
		"iteration": 2, "decision": "continue",
	})
	writeStoreEvent(t, s, store.TypeSignalInject, session, map[string]any{
		"iteration": 2, "length": 42,
	})
	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-2-spawn-0", "signal_type": "spawn", "iteration": 2,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	tests := []struct {
		id         string
		completed  bool
		inFlight   bool
		shouldSkip bool
	}{
		{"sig-1-spawn-0", true, false, true},
		{"sig-1-spawn-1", true, false, true},
		{"sig-2-spawn-0", false, true, false},
		{"sig-3-spawn-0", false, false, false},
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

	s := mustOpenStore(t)

	prov := mock.New(
		mock.WithResponses(
			mock.Response{
				Decision: "continue",
				Reason:   "next",
				Summary:  "spawn first",
				Signals: &mock.Signals{
					Spawn: json.RawMessage(`[{"run":"ralph","session":"child-1"}]`),
				},
			},
			mock.Response{
				Decision: "stop",
				Reason:   "done",
				Summary:  "spawn second",
				Signals: &mock.Signals{
					Spawn: json.RawMessage(`[{"run":"ralph","session":"child-2"}]`),
				},
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
		SpawnMaxChildren: 1,
		Store:            s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if launcher.startCalls != 1 {
		t.Fatalf("launcher start calls = %d, want 1", launcher.startCalls)
	}

	evts := readEvents(t, s, "parent-cumulative")
	spawned := filterByType(evts, store.TypeSignalSpawn)
	failed := filterByType(evts, store.TypeSignalSpawnFailed)
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
	runDir, s := tempSession(t)

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
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusPaused {
		t.Errorf("status = %v, want paused", res.Status)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1 (should stop after escalation)", res.Iterations)
	}

	row, err := s.GetSession(context.Background(), "test-escalate-pause")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusPaused {
		t.Errorf("store status = %v, want paused", row.Status)
	}
	if row.EscalationJSON == nil {
		t.Fatal("store missing escalation info")
	}
}

// TestRun_InjectSignal_ConsumedOnce verifies that inject signal text is
// applied to the next iteration's prompt and then cleared.
func TestRun_InjectSignal_ConsumedOnce(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Errorf("status = %v, want completed", res.Status)
	}

	evts := readEvents(t, s, "test-inject-once")
	injects := filterByType(evts, store.TypeSignalInject)
	if len(injects) != 1 {
		t.Fatalf("signal.inject events = %d, want 1", len(injects))
	}

	injectData := parseEventData(t, injects[0])
	if injectData["iteration"] != float64(1) {
		t.Errorf("inject iteration = %v, want 1", injectData["iteration"])
	}

	calls := mp.Calls()
	if len(calls) < 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if !containsSubstr(calls[1].Request.Prompt, "INJECTED CONTEXT HERE") {
		t.Errorf("iteration 2 prompt should contain injected context")
	}
	if containsSubstr(calls[2].Request.Prompt, "INJECTED CONTEXT HERE") {
		t.Errorf("iteration 3 prompt should NOT contain injected context (consumed)")
	}
}

// TestDispatchState_HandlerErrorDoesNotAffectCompletion verifies that
// signal.handler.error events don't interfere with dispatch state tracking.
func TestDispatchState_HandlerErrorDoesNotAffectCompletion(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "test", "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalHandlerError, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "error": "webhook timeout",
	})
	writeStoreEvent(t, s, store.TypeSignalSpawn, "test", map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-1",
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, "test")
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsCompleted("sig-1-spawn-0") {
		t.Error("signal should be completed despite handler error")
	}
	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("completed signal should be skipped on replay")
	}
}

// TestRun_ChildSessionTrackedInStore verifies that spawned child sessions
// are recorded in the parent's store child_sessions.
func TestRun_ChildSessionTrackedInStore(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-track")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}

	s := mustOpenStore(t)

	prov := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Reason:   "done",
			Summary:  "spawn child",
			Signals: &mock.Signals{
				Spawn: json.RawMessage(`[{"run":"ralph","session":"child-tracked"}]`),
			},
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
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	children, err := s.GetChildren(context.Background(), "parent-track")
	if err != nil {
		t.Fatalf("get children: %v", err)
	}

	if len(children) != 1 {
		t.Fatalf("child_sessions = %v, want 1 entry", children)
	}
	if children[0] != "child-tracked" {
		t.Errorf("child_sessions[0] = %q, want child-tracked", children[0])
	}
}
