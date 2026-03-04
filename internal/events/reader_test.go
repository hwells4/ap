package events

import (
	"path/filepath"
	"testing"
)

func TestReadAll_MissingFile(t *testing.T) {
	evts, err := ReadAll("/nonexistent/events.jsonl")
	if err != nil {
		t.Fatalf("ReadAll on missing file should not error: %v", err)
	}
	if len(evts) != 0 {
		t.Errorf("expected 0 events, got %d", len(evts))
	}
}

func TestReadAll_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := Append(path, NewEvent(TypeSessionStart, "test-session", nil, nil)); err != nil {
		t.Fatalf("write event: %v", err)
	}

	evts, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != TypeSessionStart {
		t.Errorf("event type = %q, want %q", evts[0].Type, TypeSessionStart)
	}
	if evts[0].Session != "test-session" {
		t.Errorf("event session = %q, want %q", evts[0].Session, "test-session")
	}
}

func TestReadAll_MultipleEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	events := []Event{
		NewEvent(TypeSessionStart, "test", nil, map[string]any{"key": "val1"}),
		NewEvent(TypeIterationStart, "test", nil, map[string]any{"iteration": 1}),
		NewEvent(TypeIterationComplete, "test", nil, map[string]any{"iteration": 1}),
		NewEvent(TypeSessionComplete, "test", nil, map[string]any{"reason": "done"}),
	}
	for _, evt := range events {
		if err := Append(path, evt); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}

	result, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 events, got %d", len(result))
	}

	expectedTypes := []string{TypeSessionStart, TypeIterationStart, TypeIterationComplete, TypeSessionComplete}
	for i, evt := range result {
		if evt.Type != expectedTypes[i] {
			t.Errorf("event[%d] type = %q, want %q", i, evt.Type, expectedTypes[i])
		}
	}
}

func TestReadAll_PreservesData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	err := Append(path, NewEvent(TypeSignalDispatching, "test", &Cursor{Iteration: 1}, map[string]any{
		"signal_id":   "sig-1-spawn-0",
		"signal_type": "spawn",
	}))
	if err != nil {
		t.Fatalf("write event: %v", err)
	}

	evts, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}

	evt := evts[0]
	if evt.Data["signal_id"] != "sig-1-spawn-0" {
		t.Errorf("signal_id = %v, want sig-1-spawn-0", evt.Data["signal_id"])
	}
	if evt.Data["signal_type"] != "spawn" {
		t.Errorf("signal_type = %v, want spawn", evt.Data["signal_type"])
	}
	if evt.Cursor == nil || evt.Cursor.Iteration != 1 {
		t.Errorf("cursor.iteration = %v, want 1", evt.Cursor)
	}
}
