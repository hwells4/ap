package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/store"
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

	// Verify store session content.
	row := sess.GetSession()
	if row.Status != store.StatusRunning {
		t.Errorf("Status = %q, want %q", row.Status, store.StatusRunning)
	}
	if row.Name != "test-session" {
		t.Errorf("Name = %q, want %q", row.Name, "test-session")
	}
}

func TestNewTempSession_WithState(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "paused-session", WithState(store.StatusPaused))

	row := sess.GetSession()
	if row.Status != store.StatusPaused {
		t.Errorf("Status = %q, want %q", row.Status, store.StatusPaused)
	}
}

func TestNewTempSession_WithIterations(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "iter-session",
		WithIterations(3),
		WithStageName("ralph"),
	)

	row := sess.GetSession()
	if row.IterationCompleted != 3 {
		t.Errorf("IterationCompleted = %d, want 3", row.IterationCompleted)
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
	evts := []EventSpec{
		{Type: store.TypeSessionStart, Data: map[string]any{"info": "started"}},
		{Type: store.TypeIterationStart, Data: map[string]any{"iteration": 1}},
	}

	sess := NewTempSession(t, "event-session", WithEvents(evts))

	ctx := context.Background()
	rows, err := sess.Store.GetEvents(ctx, "event-session", "", 0)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d events, want 2", len(rows))
	}
	if rows[0].Type != store.TypeSessionStart {
		t.Errorf("event[0].Type = %q, want %q", rows[0].Type, store.TypeSessionStart)
	}
	if rows[1].Type != store.TypeIterationStart {
		t.Errorf("event[1].Type = %q, want %q", rows[1].Type, store.TypeIterationStart)
	}
}

func TestNewTempSession_WithPipeline(t *testing.T) {
	t.Parallel()
	sess := NewTempSession(t, "pipeline-session", WithPipeline("go-engine-full"))

	row := sess.GetSession()
	if row.Pipeline != "go-engine-full" {
		t.Errorf("Pipeline = %q, want %q", row.Pipeline, "go-engine-full")
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
