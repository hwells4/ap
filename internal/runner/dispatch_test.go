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

func TestSignalID_Format(t *testing.T) {
	tests := []struct {
		iteration  int
		signalType string
		index      int
		want       string
	}{
		{1, "spawn", 0, "sig-1-spawn-0"},
		{3, "spawn", 1, "sig-3-spawn-1"},
		{5, "escalate", 0, "sig-5-escalate-0"},
		{10, "spawn", 2, "sig-10-spawn-2"},
	}
	for _, tt := range tests {
		got := SignalID(tt.iteration, tt.signalType, tt.index)
		if got != tt.want {
			t.Errorf("SignalID(%d, %q, %d) = %q, want %q", tt.iteration, tt.signalType, tt.index, got, tt.want)
		}
	}
}

func TestSignalID_Deterministic(t *testing.T) {
	a := SignalID(3, "spawn", 0)
	b := SignalID(3, "spawn", 0)
	if a != b {
		t.Errorf("SignalID not deterministic: %q != %q", a, b)
	}
}

func TestDispatchState_Empty(t *testing.T) {
	ds := NewDispatchState()
	if ds.IsCompleted("sig-1-spawn-0") {
		t.Error("empty state should not report completed")
	}
	if ds.IsInFlight("sig-1-spawn-0") {
		t.Error("empty state should not report in-flight")
	}
	if ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("empty state should not skip")
	}
}

func TestDispatchState_Completed(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "test-completed"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawn, session, map[string]any{
		"signal_id":     "sig-1-spawn-0",
		"iteration":     1,
		"child_session": "child-1",
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsCompleted("sig-1-spawn-0") {
		t.Error("expected sig-1-spawn-0 to be completed")
	}
	if ds.IsInFlight("sig-1-spawn-0") {
		t.Error("completed signal should not be in-flight")
	}
	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("completed signal should be skipped")
	}
}

func TestDispatchState_InFlight(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "test-inflight"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id":   "sig-2-spawn-0",
		"signal_type": "spawn",
		"iteration":   2,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if ds.IsCompleted("sig-2-spawn-0") {
		t.Error("in-flight signal should not be completed")
	}
	if !ds.IsInFlight("sig-2-spawn-0") {
		t.Error("expected sig-2-spawn-0 to be in-flight")
	}
	if ds.ShouldSkip("sig-2-spawn-0") {
		t.Error("in-flight signal should not be skipped")
	}
}

func TestDispatchState_FailedSignalIsCompleted(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "test-failed"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawnFailed, session, map[string]any{
		"signal_id": "sig-1-spawn-0",
		"iteration": 1,
		"error":     "stage not found",
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsCompleted("sig-1-spawn-0") {
		t.Error("failed signal should be marked completed")
	}
	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("failed signal should be skipped")
	}
}

func TestDispatchState_EscalateCompleted(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "test-esc-completed"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id":   "sig-1-escalate-0",
		"signal_type": "escalate",
		"iteration":   1,
	})
	writeStoreEvent(t, s, store.TypeSignalEscalate, session, map[string]any{
		"signal_id": "sig-1-escalate-0",
		"iteration": 1,
		"type":      "human",
		"reason":    "review",
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsCompleted("sig-1-escalate-0") {
		t.Error("escalate signal should be completed")
	}
	if !ds.ShouldSkip("sig-1-escalate-0") {
		t.Error("completed escalate should be skipped")
	}
}

func TestDispatchState_EmptyStore(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "test-empty"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore on empty store should not error: %v", err)
	}
	if ds.IsCompleted("sig-1-spawn-0") {
		t.Error("empty dispatch state should not report completed")
	}
}

func TestRun_SpawnSignal_EmitsDispatchingBeforeSpawn(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "parent-dispatch")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := mustOpenStore(t)

	prov := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Reason:   "done",
			Summary:  "spawn child",
			Signals: &mock.Signals{
				Spawn: json.RawMessage(`[{"run":"ralph:2","session":"child-dispatch"}]`),
			},
		}),
	)

	launcher := &spawnTestLauncher{}
	_, err := Run(context.Background(), Config{
		Session:        "parent-dispatch",
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

	evts := readEvents(t, s, "parent-dispatch")

	dispatchIdx := -1
	spawnIdx := -1
	for i, evt := range evts {
		switch evt.Type {
		case store.TypeSignalDispatching:
			dispatchIdx = i
		case store.TypeSignalSpawn:
			spawnIdx = i
		}
	}

	if dispatchIdx == -1 {
		t.Fatal("signal.dispatching event not found")
	}
	if spawnIdx == -1 {
		t.Fatal("signal.spawn event not found")
	}
	if dispatchIdx >= spawnIdx {
		t.Errorf("signal.dispatching (idx=%d) should precede signal.spawn (idx=%d)", dispatchIdx, spawnIdx)
	}

	dispatchData := parseEventData(t, evts[dispatchIdx])
	spawnData := parseEventData(t, evts[spawnIdx])
	if dispatchData["signal_id"] != "sig-1-spawn-0" {
		t.Errorf("dispatching signal_id = %v, want sig-1-spawn-0", dispatchData["signal_id"])
	}
	if spawnData["signal_id"] != "sig-1-spawn-0" {
		t.Errorf("spawn signal_id = %v, want sig-1-spawn-0", spawnData["signal_id"])
	}
	if dispatchData["signal_type"] != "spawn" {
		t.Errorf("dispatching signal_type = %v, want spawn", dispatchData["signal_type"])
	}
}

func TestRun_EscalateSignal_EmitsDispatchingBeforeEscalate(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "work done", "human", "review needed", []string{"approve", "reject"}),
		),
	)

	_, err := Run(context.Background(), Config{
		Session:        "test-escalate-dispatch",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     5,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-escalate-dispatch")

	dispatchIdx := -1
	escalateIdx := -1
	for i, evt := range evts {
		switch evt.Type {
		case store.TypeSignalDispatching:
			dispatchIdx = i
		case store.TypeSignalEscalate:
			escalateIdx = i
		}
	}

	if dispatchIdx == -1 {
		t.Fatal("signal.dispatching event not found")
	}
	if escalateIdx == -1 {
		t.Fatal("signal.escalate event not found")
	}
	if dispatchIdx >= escalateIdx {
		t.Errorf("signal.dispatching (idx=%d) should precede signal.escalate (idx=%d)", dispatchIdx, escalateIdx)
	}

	dispatchData := parseEventData(t, evts[dispatchIdx])
	escalateData := parseEventData(t, evts[escalateIdx])
	if dispatchData["signal_id"] != "sig-1-escalate-0" {
		t.Errorf("dispatching signal_id = %v, want sig-1-escalate-0", dispatchData["signal_id"])
	}
	if escalateData["signal_id"] != "sig-1-escalate-0" {
		t.Errorf("escalate signal_id = %v, want sig-1-escalate-0", escalateData["signal_id"])
	}
}

func TestIntegration_CrashBetweenDispatchAndCompletion(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "crash-test"
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
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
		"iteration":   1,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.IsInFlight("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 should be in-flight after crash")
	}
	if ds.IsCompleted("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 should NOT be completed after crash")
	}
	if ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 should NOT be skipped (needs re-dispatch)")
	}

	writeStoreEvent(t, s, store.TypeSignalSpawn, session, map[string]any{
		"signal_id":     "sig-1-spawn-0",
		"iteration":     1,
		"child_session": "child-recovered",
	})

	ds2, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore after recovery: %v", err)
	}

	if !ds2.IsCompleted("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 should be completed after recovery")
	}
	if ds2.IsInFlight("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 should NOT be in-flight after recovery")
	}
}

func TestIntegration_MultipleSignals_MixedState(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	session := "multi"
	s.CreateSession(ctx, session, "stage", "test", "{}")

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-1-spawn-0", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawn, session, map[string]any{
		"signal_id": "sig-1-spawn-0", "child_session": "child-1",
	})

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-1-spawn-1", "signal_type": "spawn", "iteration": 1,
	})
	writeStoreEvent(t, s, store.TypeSignalSpawnFailed, session, map[string]any{
		"signal_id": "sig-1-spawn-1", "error": "stage not found",
	})

	writeStoreEvent(t, s, store.TypeSignalDispatching, session, map[string]any{
		"signal_id": "sig-2-spawn-0", "signal_type": "spawn", "iteration": 2,
	})

	ds, err := LoadDispatchStateFromStore(ctx, s, session)
	if err != nil {
		t.Fatalf("LoadDispatchStateFromStore: %v", err)
	}

	if !ds.ShouldSkip("sig-1-spawn-0") {
		t.Error("sig-1-spawn-0 (completed) should be skipped")
	}
	if !ds.ShouldSkip("sig-1-spawn-1") {
		t.Error("sig-1-spawn-1 (failed) should be skipped")
	}
	if ds.ShouldSkip("sig-2-spawn-0") {
		t.Error("sig-2-spawn-0 (in-flight) should NOT be skipped")
	}
	if !ds.IsInFlight("sig-2-spawn-0") {
		t.Error("sig-2-spawn-0 should be in-flight")
	}
}

// writeStoreEvent is a test helper that appends an event to the store.
func writeStoreEvent(t *testing.T, s *store.Store, eventType, session string, data map[string]any) {
	t.Helper()
	dataJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal event data: %v", err)
	}
	if err := s.AppendEvent(context.Background(), session, eventType, "{}", string(dataJSON)); err != nil {
		t.Fatalf("write event %s: %v", eventType, err)
	}
}
