package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/state"
)

func TestNewTempSession_Defaults(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "test-session")

	if sess.Name != "test-session" {
		t.Errorf("Name = %q, want %q", sess.Name, "test-session")
	}

	// Verify directory structure exists.
	if _, err := os.Stat(sess.Dir); err != nil {
		t.Errorf("session dir missing: %v", err)
	}
	if _, err := os.Stat(sess.StatePath); err != nil {
		t.Errorf("state.json missing: %v", err)
	}
	if _, err := os.Stat(sess.EventsPath); err != nil {
		t.Errorf("events.jsonl missing: %v", err)
	}

	// Verify state.json content.
	ss := sess.LoadState()
	if ss.Status != state.StateRunning {
		t.Errorf("Status = %q, want %q", ss.Status, state.StateRunning)
	}
	if ss.Session != "test-session" {
		t.Errorf("Session = %q, want %q", ss.Session, "test-session")
	}
}

func TestNewTempSession_WithState(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "paused-session", WithState(state.StatePaused))

	ss := sess.LoadState()
	if ss.Status != state.StatePaused {
		t.Errorf("Status = %q, want %q", ss.Status, state.StatePaused)
	}
}

func TestNewTempSession_WithIterations(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "iter-session",
		WithIterations(3),
		WithStageName("ralph"),
	)

	ss := sess.LoadState()
	if ss.IterationCompleted != 3 {
		t.Errorf("IterationCompleted = %d, want 3", ss.IterationCompleted)
	}

	// Verify iteration directories exist.
	for i := 1; i <= 3; i++ {
		iterDir := sess.IterationDir("ralph", i)
		if _, err := os.Stat(iterDir); err != nil {
			t.Errorf("iteration %d dir missing: %v", i, err)
		}
		statusPath := sess.StatusPathFor("ralph", i)
		if _, err := os.Stat(statusPath); err != nil {
			t.Errorf("iteration %d status.json missing: %v", i, err)
		}
	}
}

func TestNewTempSession_WithEvents(t *testing.T) {
	t.Parallel()
	evts := []events.Event{
		{Timestamp: "2026-01-01T00:00:00Z", Type: events.TypeSessionStart, Session: "test"},
		{Timestamp: "2026-01-01T00:01:00Z", Type: events.TypeIterationStart, Session: "test"},
	}

	sess := NewTempSession(t, "event-session", WithEvents(evts))

	data, err := os.ReadFile(sess.EventsPath)
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	if len(data) == 0 {
		t.Error("events.jsonl is empty")
	}
}

func TestNewTempSession_WithPipeline(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "pipeline-session", WithPipeline("go-engine-full"))

	ss := sess.LoadState()
	if ss.Pipeline != "go-engine-full" {
		t.Errorf("Pipeline = %q, want %q", ss.Pipeline, "go-engine-full")
	}
}

func TestNewTempSession_LocksDir(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "lock-session")

	locksDir := filepath.Join(sess.RootDir, ".ap", "locks")
	if _, err := os.Stat(locksDir); err != nil {
		t.Errorf("locks dir missing: %v", err)
	}
}
