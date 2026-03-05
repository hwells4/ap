package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/store"
)

// TempSession creates a .ap/runs/{session}/ tree in t.TempDir()
// with configurable pre-populated state, events, and iterations.
type TempSession struct {
	// Dir is the root session directory (.ap/runs/{session}).
	Dir string

	// RootDir is the project root (parent of .ap/).
	RootDir string

	// Store is the in-memory store backing this session.
	Store *store.Store

	// Name is the session name.
	Name string

	t *testing.T
}

// SessionOption configures a TempSession.
type SessionOption func(*sessionConfig)

// EventSpec describes an event to insert into the store.
type EventSpec struct {
	Type    string
	Cursor  map[string]any
	Data    map[string]any
}

type sessionConfig struct {
	status     string
	iterations int
	evts       []EventSpec
	stageName  string
	pipeline   string
}

// WithState sets the session status. Use store.Status* constants
// (e.g. store.StatusRunning, store.StatusPaused).
func WithState(s string) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.status = s
	}
}

// WithIterations pre-creates the specified number of completed iteration directories.
func WithIterations(n int) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.iterations = n
	}
}

// WithEvents appends the given events to the store.
func WithEvents(evts []EventSpec) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.evts = evts
	}
}

// WithStageName sets the stage name for the session.
func WithStageName(name string) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.stageName = name
	}
}

// WithPipeline sets the pipeline name for the session.
func WithPipeline(name string) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.pipeline = name
	}
}

// NewTempSession creates a session directory tree in t.TempDir()
// and an in-memory store with the session pre-populated.
// The directory is automatically cleaned up when the test completes.
func NewTempSession(t *testing.T, sessionName string, opts ...SessionOption) *TempSession {
	t.Helper()

	cfg := &sessionConfig{
		status:    store.StatusRunning,
		stageName: "default",
		pipeline:  "test-pipeline",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	rootDir := t.TempDir()
	sessionDir := filepath.Join(rootDir, ".ap", "runs", sessionName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("testutil: create session dir: %v", err)
	}

	// Create locks directory.
	locksDir := filepath.Join(rootDir, ".ap", "locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatalf("testutil: create locks dir: %v", err)
	}

	// Open in-memory store and create the session.
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("testutil: open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx := context.Background()
	if err := s.CreateSession(ctx, sessionName, "loop", cfg.pipeline, "{}"); err != nil {
		t.Fatalf("testutil: create session in store: %v", err)
	}

	// Apply status and iteration updates.
	updates := map[string]any{
		"current_stage":       cfg.stageName,
		"iteration":           cfg.iterations,
		"iteration_completed": cfg.iterations,
	}
	if cfg.status != store.StatusRunning {
		updates["status"] = cfg.status
	}
	if err := s.UpdateSession(ctx, sessionName, updates); err != nil {
		t.Fatalf("testutil: update session in store: %v", err)
	}

	// Create stage directory and iteration subdirectories.
	stageDir := filepath.Join(sessionDir, fmt.Sprintf("stage-00-%s", cfg.stageName))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatalf("testutil: create stage dir: %v", err)
	}

	for i := 1; i <= cfg.iterations; i++ {
		iterDir := filepath.Join(stageDir, "iterations", fmt.Sprintf("%03d", i))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			t.Fatalf("testutil: create iteration dir: %v", err)
		}
		// Write a minimal status.json for each iteration.
		statusData := map[string]any{
			"decision": "continue",
			"summary":  fmt.Sprintf("iteration %d completed", i),
			"work":     map[string]any{"items_completed": []string{}, "files_touched": []string{}},
			"errors":   []string{},
		}
		data, jsonErr := json.MarshalIndent(statusData, "", "  ")
		if jsonErr != nil {
			t.Fatalf("testutil: marshal iteration status: %v", jsonErr)
		}
		statusPath := filepath.Join(iterDir, "status.json")
		if err := os.WriteFile(statusPath, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("testutil: write iteration status: %v", err)
		}
	}

	// Append events to the store.
	for _, evt := range cfg.evts {
		cursorJSON := "{}"
		if evt.Cursor != nil {
			if b, err := json.Marshal(evt.Cursor); err == nil {
				cursorJSON = string(b)
			}
		}
		dataJSON := "{}"
		if evt.Data != nil {
			if b, err := json.Marshal(evt.Data); err == nil {
				dataJSON = string(b)
			}
		}
		if err := s.AppendEvent(ctx, sessionName, evt.Type, cursorJSON, dataJSON); err != nil {
			t.Fatalf("testutil: append event: %v", err)
		}
	}

	return &TempSession{
		Dir:     sessionDir,
		RootDir: rootDir,
		Store:   s,
		Name:    sessionName,
		t:       t,
	}
}

// IterationDir returns the path for a specific iteration directory.
func (s *TempSession) IterationDir(stage string, iteration int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("stage-00-%s", stage), "iterations", fmt.Sprintf("%03d", iteration))
}

// StatusPathFor returns the status.json path for a specific iteration.
func (s *TempSession) StatusPathFor(stage string, iteration int) string {
	return filepath.Join(s.IterationDir(stage, iteration), "status.json")
}

// GetSession reads and returns the current session row from the store.
func (s *TempSession) GetSession() *store.SessionRow {
	s.t.Helper()
	ctx := context.Background()
	row, err := s.Store.GetSession(ctx, s.Name)
	if err != nil {
		s.t.Fatalf("testutil: get session: %v", err)
	}
	return row
}

// StartedAt is the fixed time used for test sessions.
var StartedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
