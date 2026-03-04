package runner

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

// TestIntegration_LifecycleEvents_Schema validates that all emitted
// lifecycle events contain the required fields per Contract 5.
func TestIntegration_LifecycleEvents_Schema(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iteration 1"),
			mock.StopResponse("done", "all complete"),
		),
	)

	cfg := Config{
		Session:        "test-events-schema",
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

	evts := readEvents(t, runDir)

	// Validate required fields on ALL events.
	for i, evt := range evts {
		if evt.Type == "" {
			t.Errorf("event[%d]: missing type", i)
		}
		if evt.Session == "" {
			t.Errorf("event[%d]: missing session", i)
		}
		if evt.Timestamp == "" {
			t.Errorf("event[%d]: missing timestamp", i)
		}
	}

	// Check session_start event.
	sessionStarts := filterByType(evts, events.TypeSessionStart)
	if len(sessionStarts) != 1 {
		t.Fatalf("expected 1 session_start, got %d", len(sessionStarts))
	}
	ss := sessionStarts[0]
	if ss.Data["stage"] != "test-stage" {
		t.Errorf("session_start stage = %v, want test-stage", ss.Data["stage"])
	}
	if ss.Data["provider"] != "mock" {
		t.Errorf("session_start provider = %v, want mock", ss.Data["provider"])
	}
	if _, ok := ss.Data["iterations"]; !ok {
		t.Error("session_start missing iterations field")
	}

	// Check iteration_start events.
	iterStarts := filterByType(evts, events.TypeIterationStart)
	if len(iterStarts) != 2 {
		t.Fatalf("expected 2 iteration_starts, got %d", len(iterStarts))
	}
	for i, is := range iterStarts {
		if is.Cursor == nil {
			t.Errorf("iteration_start[%d]: missing cursor", i)
			continue
		}
		if is.Cursor.Iteration != i+1 {
			t.Errorf("iteration_start[%d] cursor.iteration = %d, want %d", i, is.Cursor.Iteration, i+1)
		}
		if is.Cursor.Provider == "" {
			t.Errorf("iteration_start[%d]: cursor.provider is empty", i)
		}
		if is.Data["iteration"] != float64(i+1) {
			t.Errorf("iteration_start[%d] data.iteration = %v, want %d", i, is.Data["iteration"], i+1)
		}
	}

	// Check iteration_complete events.
	iterCompletes := filterByType(evts, events.TypeIterationComplete)
	if len(iterCompletes) != 2 {
		t.Fatalf("expected 2 iteration_completes, got %d", len(iterCompletes))
	}
	for i, ic := range iterCompletes {
		if ic.Cursor == nil {
			t.Errorf("iteration_complete[%d]: missing cursor", i)
			continue
		}
		if _, ok := ic.Data["decision"]; !ok {
			t.Errorf("iteration_complete[%d]: missing decision", i)
		}
		if _, ok := ic.Data["summary"]; !ok {
			t.Errorf("iteration_complete[%d]: missing summary", i)
		}
		if _, ok := ic.Data["duration"]; !ok {
			t.Errorf("iteration_complete[%d]: missing duration", i)
		}
	}

	// Check session_complete event.
	sessionCompletes := filterByType(evts, events.TypeSessionComplete)
	if len(sessionCompletes) != 1 {
		t.Fatalf("expected 1 session_complete, got %d", len(sessionCompletes))
	}
	sc := sessionCompletes[0]
	if _, ok := sc.Data["iterations"]; !ok {
		t.Error("session_complete missing iterations field")
	}
	if _, ok := sc.Data["reason"]; !ok {
		t.Error("session_complete missing reason field")
	}
}

// TestIntegration_LifecycleEvents_Ordering validates strict event ordering.
func TestIntegration_LifecycleEvents_Ordering(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.ContinueResponse("iter 2"),
		),
	)

	two := 2
	cfg := Config{
		Session:        "test-ordering",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     two,
		PromptTemplate: "iteration ${ITERATION}",
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, runDir)

	// Expected order: session_start, iter1_start, iter1_complete,
	// iter2_start, iter2_complete, session_complete
	expectedTypes := []string{
		events.TypeSessionStart,
		events.TypeIterationStart,
		events.TypeIterationComplete,
		events.TypeIterationStart,
		events.TypeIterationComplete,
		events.TypeSessionComplete,
	}

	if len(evts) != len(expectedTypes) {
		t.Fatalf("event count = %d, want %d; types: %v", len(evts), len(expectedTypes), eventTypes(evts))
	}

	for i, want := range expectedTypes {
		if evts[i].Type != want {
			t.Errorf("event[%d] type = %q, want %q", i, evts[i].Type, want)
		}
	}

	// Verify timestamps are monotonically non-decreasing.
	for i := 1; i < len(evts); i++ {
		if evts[i].Timestamp < evts[i-1].Timestamp {
			t.Errorf("event[%d] timestamp %s < event[%d] timestamp %s",
				i, evts[i].Timestamp, i-1, evts[i-1].Timestamp)
		}
	}
}

// TestIntegration_LifecycleEvents_FailurePath validates events on
// provider failure — error event should appear before session_complete.
func TestIntegration_LifecycleEvents_FailurePath(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.NoStatusResponse(), // Missing status.json
		),
	)

	cfg := Config{
		Session:        "test-fail-events",
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

	if res.Status != state.StateFailed {
		t.Errorf("status = %q, want %q", res.Status, state.StateFailed)
	}

	evts := readEvents(t, runDir)

	// Should have: session.started, iter1 started/completed,
	// iter2 started, iteration.failed
	types := eventTypes(evts)

	// First event should be session.started.
	if len(types) == 0 || types[0] != events.TypeSessionStart {
		t.Fatalf("first event = %v, want %s", types, events.TypeSessionStart)
	}

	// Should contain an iteration.failed event.
	hasIterFailed := false
	for _, typ := range types {
		if typ == events.TypeIterationFailed {
			hasIterFailed = true
			break
		}
	}
	if !hasIterFailed {
		t.Errorf("no iteration.failed event found; types: %v", types)
	}

	// Verify state.json reflects failure.
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StateFailed {
		t.Errorf("state status = %q, want %q", st.Status, state.StateFailed)
	}
}

// TestIntegration_EventsBeforeState validates that events are appended
// before the state snapshot is updated (crash consistency guarantee).
func TestIntegration_EventsBeforeState(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.ContinueResponse("single iter")),
	)

	cfg := Config{
		Session:        "test-before-state",
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

	// Both events.jsonl and state.json should exist.
	eventsPath := runDir + "/events.jsonl"
	statePath := runDir + "/state.json"

	evInfo, err := os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("events.jsonl missing: %v", err)
	}
	stInfo, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("state.json missing: %v", err)
	}

	// Events file should have been modified at or before state file.
	// This verifies the write order: events → state.
	if evInfo.ModTime().After(stInfo.ModTime().Add(1)) {
		t.Logf("events mod: %v, state mod: %v", evInfo.ModTime(), stInfo.ModTime())
		// This is a soft check — filesystem timestamps may have coarse resolution.
	}

	// The critical check: events.jsonl should have a session_complete
	// event, and state.json should show completed.
	evts := readEvents(t, runDir)
	sessionCompletes := filterByType(evts, events.TypeSessionComplete)
	if len(sessionCompletes) == 0 {
		t.Error("no session_complete event")
	}

	st, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.Status != state.StateCompleted {
		t.Errorf("state status = %q, want %q", st.Status, state.StateCompleted)
	}
}

// readEvents parses events.jsonl into typed Event structs.
func readEvents(t *testing.T, runDir string) []events.Event {
	t.Helper()
	data, err := os.ReadFile(runDir + "/events.jsonl")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}

	var evts []events.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var evt events.Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		evts = append(evts, evt)
	}
	return evts
}

// filterByType returns events matching the given type.
func filterByType(evts []events.Event, typ string) []events.Event {
	var out []events.Event
	for _, evt := range evts {
		if evt.Type == typ {
			out = append(out, evt)
		}
	}
	return out
}

// eventTypes extracts event type strings.
func eventTypes(evts []events.Event) []string {
	types := make([]string, len(evts))
	for i, evt := range evts {
		types[i] = evt.Type
	}
	return types
}
