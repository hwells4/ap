package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/state"
)

// TempSession creates a .ap/runs/{session}/ tree in t.TempDir()
// with configurable pre-populated state, events, and iterations.
type TempSession struct {
	// Dir is the root session directory (.ap/runs/{session}).
	Dir string

	// RootDir is the project root (parent of .ap/).
	RootDir string

	// StatePath is the path to state.json.
	StatePath string

	// EventsPath is the path to events.jsonl.
	EventsPath string

	// Name is the session name.
	Name string

	t *testing.T
}

// SessionOption configures a TempSession.
type SessionOption func(*sessionConfig)

type sessionConfig struct {
	state      state.State
	iterations int
	evts       []events.Event
	stageName  string
	pipeline   string
}

// WithState sets the session state in state.json.
func WithState(s state.State) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.state = s
	}
}

// WithIterations pre-creates the specified number of completed iteration directories.
func WithIterations(n int) SessionOption {
	return func(cfg *sessionConfig) {
		cfg.iterations = n
	}
}

// WithEvents writes the given events to events.jsonl.
func WithEvents(evts []events.Event) SessionOption {
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

// NewTempSession creates a session directory tree in t.TempDir().
// The directory is automatically cleaned up when the test completes.
func NewTempSession(t *testing.T, sessionName string, opts ...SessionOption) *TempSession {
	t.Helper()

	cfg := &sessionConfig{
		state:     state.StateRunning,
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

	statePath := filepath.Join(sessionDir, "state.json")
	eventsPath := filepath.Join(sessionDir, "events.jsonl")

	// Write state.json.
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ss := &state.SessionState{
		Session:            sessionName,
		Type:               "loop",
		Pipeline:           cfg.pipeline,
		Status:             cfg.state,
		Iteration:          cfg.iterations,
		IterationCompleted: cfg.iterations,
		StartedAt:          startedAt.Format(time.RFC3339),
		CurrentStage:       cfg.stageName,
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	}
	if err := state.Write(statePath, ss); err != nil {
		t.Fatalf("testutil: write state.json: %v", err)
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
		data, err := json.MarshalIndent(statusData, "", "  ")
		if err != nil {
			t.Fatalf("testutil: marshal iteration status: %v", err)
		}
		statusPath := filepath.Join(iterDir, "status.json")
		if err := os.WriteFile(statusPath, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("testutil: write iteration status: %v", err)
		}
	}

	// Write events.jsonl.
	if len(cfg.evts) > 0 {
		w := events.NewWriter(eventsPath)
		for _, evt := range cfg.evts {
			if err := w.Append(evt); err != nil {
				t.Fatalf("testutil: write event: %v", err)
			}
		}
	} else {
		// Create empty events file.
		if err := os.WriteFile(eventsPath, nil, 0o644); err != nil {
			t.Fatalf("testutil: create events file: %v", err)
		}
	}

	return &TempSession{
		Dir:        sessionDir,
		RootDir:    rootDir,
		StatePath:  statePath,
		EventsPath: eventsPath,
		Name:       sessionName,
		t:          t,
	}
}

// IterationDir returns the path for a specific iteration directory.
func (s *TempSession) IterationDir(stage string, iteration int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("stage-00-%s", stage), "iterations", fmt.Sprintf("%03d", iteration))
}

// StatusPath returns the status.json path for a specific iteration.
func (s *TempSession) StatusPathFor(stage string, iteration int) string {
	return filepath.Join(s.IterationDir(stage, iteration), "status.json")
}

// LoadState reads and returns the current state.json.
func (s *TempSession) LoadState() *state.SessionState {
	s.t.Helper()
	ss, err := state.Load(s.StatePath)
	if err != nil {
		s.t.Fatalf("testutil: load state: %v", err)
	}
	return ss
}
