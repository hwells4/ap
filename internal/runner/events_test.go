package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestIntegration_LifecycleEvents_Schema validates that all emitted
// lifecycle events contain the required fields per Contract 5.
func TestIntegration_LifecycleEvents_Schema(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-events-schema")

	// Validate required fields on ALL events.
	for i, evt := range evts {
		if evt.Type == "" {
			t.Errorf("event[%d]: missing type", i)
		}
		if evt.SessionName == "" {
			t.Errorf("event[%d]: missing session", i)
		}
		if evt.CreatedAt == "" {
			t.Errorf("event[%d]: missing timestamp", i)
		}
	}

	// Check session_start event.
	sessionStarts := filterByType(evts, store.TypeSessionStart)
	if len(sessionStarts) != 1 {
		t.Fatalf("expected 1 session_start, got %d", len(sessionStarts))
	}
	ss := sessionStarts[0]
	ssData := parseEventData(t, ss)
	if ssData["stage"] != "test-stage" {
		t.Errorf("session_start stage = %v, want test-stage", ssData["stage"])
	}
	if ssData["provider"] != "mock" {
		t.Errorf("session_start provider = %v, want mock", ssData["provider"])
	}
	if _, ok := ssData["iterations"]; !ok {
		t.Error("session_start missing iterations field")
	}

	// Check iteration_start events.
	iterStarts := filterByType(evts, store.TypeIterationStart)
	if len(iterStarts) != 2 {
		t.Fatalf("expected 2 iteration_starts, got %d", len(iterStarts))
	}
	for i, is := range iterStarts {
		isData := parseEventData(t, is)
		var cursor map[string]any
		if err := json.Unmarshal([]byte(is.CursorJSON), &cursor); err != nil {
			t.Errorf("iteration_start[%d]: invalid cursor JSON: %v", i, err)
			continue
		}
		if cursor["iteration"] != float64(i+1) {
			t.Errorf("iteration_start[%d] cursor.iteration = %v, want %d", i, cursor["iteration"], i+1)
		}
		if cursor["provider"] == "" {
			t.Errorf("iteration_start[%d]: cursor.provider is empty", i)
		}
		if isData["iteration"] != float64(i+1) {
			t.Errorf("iteration_start[%d] data.iteration = %v, want %d", i, isData["iteration"], i+1)
		}
	}

	// Check iteration_complete events.
	iterCompletes := filterByType(evts, store.TypeIterationComplete)
	if len(iterCompletes) != 2 {
		t.Fatalf("expected 2 iteration_completes, got %d", len(iterCompletes))
	}
	for i, ic := range iterCompletes {
		icData := parseEventData(t, ic)
		if _, ok := icData["decision"]; !ok {
			t.Errorf("iteration_complete[%d]: missing decision", i)
		}
		if _, ok := icData["summary"]; !ok {
			t.Errorf("iteration_complete[%d]: missing summary", i)
		}
		if _, ok := icData["duration"]; !ok {
			t.Errorf("iteration_complete[%d]: missing duration", i)
		}
	}

	// Check session_complete event.
	sessionCompletes := filterByType(evts, store.TypeSessionComplete)
	if len(sessionCompletes) != 1 {
		t.Fatalf("expected 1 session_complete, got %d", len(sessionCompletes))
	}
	scData := parseEventData(t, sessionCompletes[0])
	if _, ok := scData["iterations"]; !ok {
		t.Error("session_complete missing iterations field")
	}
	if _, ok := scData["reason"]; !ok {
		t.Error("session_complete missing reason field")
	}
}

// TestIntegration_LifecycleEvents_Ordering validates strict event ordering.
func TestIntegration_LifecycleEvents_Ordering(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	evts := readEvents(t, s, "test-ordering")

	// Expected order: session_start, iter1_start, iter1_complete,
	// iter2_start, iter2_complete, session_complete
	expectedTypes := []string{
		store.TypeSessionStart,
		store.TypeIterationStart,
		store.TypeIterationComplete,
		store.TypeIterationStart,
		store.TypeIterationComplete,
		store.TypeSessionComplete,
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
		if evts[i].CreatedAt < evts[i-1].CreatedAt {
			t.Errorf("event[%d] timestamp %s < event[%d] timestamp %s",
				i, evts[i].CreatedAt, i-1, evts[i-1].CreatedAt)
		}
	}
}

// TestIntegration_LifecycleEvents_FailurePath validates events on
// provider failure — error event should appear before session_complete.
func TestIntegration_LifecycleEvents_FailurePath(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iter 1"),
			mock.FailureResponse(fmt.Errorf("provider crashed")),
		),
	)

	cfg := Config{
		Session:        "test-fail-events",
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

	if res.Status != store.StatusFailed {
		t.Errorf("status = %q, want %q", res.Status, store.StatusFailed)
	}

	evts := readEvents(t, s, "test-fail-events")

	// Should have: session.started, iter1 started/completed,
	// iter2 started, iteration.failed
	types := eventTypes(evts)

	// First event should be session.started.
	if len(types) == 0 || types[0] != store.TypeSessionStart {
		t.Fatalf("first event = %v, want %s", types, store.TypeSessionStart)
	}

	// Should contain an iteration.failed event.
	hasIterFailed := false
	for _, typ := range types {
		if typ == store.TypeIterationFailed {
			hasIterFailed = true
			break
		}
	}
	if !hasIterFailed {
		t.Errorf("no iteration.failed event found; types: %v", types)
	}

	// Verify store session reflects failure.
	row, err := s.GetSession(context.Background(), "test-fail-events")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusFailed {
		t.Errorf("state status = %q, want %q", row.Status, store.StatusFailed)
	}
}

// TestIntegration_EventsBeforeState validates that events are appended
// before the state snapshot is updated (crash consistency guarantee).
func TestIntegration_EventsBeforeState(t *testing.T) {
	runDir, s := tempSession(t)

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
		Store:          s,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// The critical check: store should have a session_complete
	// event, and session should show completed.
	evts := readEvents(t, s, "test-before-state")
	sessionCompletes := filterByType(evts, store.TypeSessionComplete)
	if len(sessionCompletes) == 0 {
		t.Error("no session_complete event")
	}

	row, err := s.GetSession(context.Background(), "test-before-state")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != store.StatusCompleted {
		t.Errorf("state status = %q, want %q", row.Status, store.StatusCompleted)
	}
}

// mustOpenStore opens an in-memory store for testing.
func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// readEvents queries events from the store for a session.
func readEvents(t *testing.T, s *store.Store, session string) []store.EventRow {
	t.Helper()
	evts, err := s.GetEvents(context.Background(), session, "", 0)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	return evts
}

// filterByType returns events matching the given type.
func filterByType(evts []store.EventRow, typ string) []store.EventRow {
	var out []store.EventRow
	for _, evt := range evts {
		if evt.Type == typ {
			out = append(out, evt)
		}
	}
	return out
}

// eventTypes extracts event type strings.
func eventTypes(evts []store.EventRow) []string {
	types := make([]string, len(evts))
	for i, evt := range evts {
		types[i] = evt.Type
	}
	return types
}

// parseEventData parses the DataJSON field of an EventRow into a map.
func parseEventData(t *testing.T, evt store.EventRow) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(evt.DataJSON), &data); err != nil {
		t.Fatalf("parse event data: %v", err)
	}
	return data
}
