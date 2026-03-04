package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/state"
)

// setupCleanSession creates a session dir with state.json and optional data files.
func setupCleanSession(t *testing.T, dir, session string, st *state.SessionState) {
	t.Helper()
	sessionDir := filepath.Join(dir, ".ap", "runs", session)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if err := state.Write(statePath, st); err != nil {
		t.Fatal(err)
	}
	// Write a dummy file so we have bytes to free.
	dummyPath := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(dummyPath, []byte(`{"type":"session.started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCleanCompletedSession(t *testing.T) {
	dir := t.TempDir()
	completedAt := "2026-03-04T02:00:00Z"
	setupCleanSession(t, dir, "done-sess", &state.SessionState{
		Session:     "done-sess",
		Status:      state.StateCompleted,
		StartedAt:   "2026-03-04T00:00:00Z",
		CompletedAt: &completedAt,
		Stages:      []state.StageState{},
		History:     []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "done-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	cleaned, ok := result["cleaned"].([]any)
	if !ok || len(cleaned) != 1 {
		t.Fatalf("expected 1 cleaned session, got: %#v", result["cleaned"])
	}
	entry := cleaned[0].(map[string]any)
	if entry["session"] != "done-sess" {
		t.Fatalf("cleaned session = %v, want done-sess", entry["session"])
	}

	// Verify session dir is removed.
	sessionDir := filepath.Join(dir, ".ap", "runs", "done-sess")
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed, stat = %v", err)
	}
}

func TestCleanFailedSession(t *testing.T) {
	dir := t.TempDir()
	errMsg := "provider crash"
	errType := "provider_failed"
	setupCleanSession(t, dir, "fail-sess", &state.SessionState{
		Session:   "fail-sess",
		Status:    state.StateFailed,
		StartedAt: "2026-03-04T00:00:00Z",
		Error:     &errMsg,
		ErrorType: &errType,
		Stages:    []state.StageState{},
		History:   []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "fail-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &result)
	cleaned := result["cleaned"].([]any)
	if len(cleaned) != 1 {
		t.Fatalf("expected 1 cleaned session, got %d", len(cleaned))
	}
}

func TestCleanRunningSessionBlocked(t *testing.T) {
	dir := t.TempDir()
	setupCleanSession(t, dir, "running-sess", &state.SessionState{
		Session:   "running-sess",
		Status:    state.StateRunning,
		StartedAt: "2026-03-04T00:00:00Z",
		Stages:    []state.StageState{},
		History:   []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "running-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &result)

	skipped, ok := result["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("expected 1 skipped session, got: %#v", result["skipped"])
	}
	entry := skipped[0].(map[string]any)
	if entry["session"] != "running-sess" {
		t.Fatalf("skipped session = %v, want running-sess", entry["session"])
	}
	if entry["reason"] != "running" {
		t.Fatalf("skip reason = %v, want running", entry["reason"])
	}

	// Verify session dir still exists.
	sessionDir := filepath.Join(dir, ".ap", "runs", "running-sess")
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist: %v", err)
	}
}

func TestCleanRunningSessionForce(t *testing.T) {
	dir := t.TempDir()
	setupCleanSession(t, dir, "force-sess", &state.SessionState{
		Session:   "force-sess",
		Status:    state.StateRunning,
		StartedAt: "2026-03-04T00:00:00Z",
		Stages:    []state.StageState{},
		History:   []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "force-sess", "--force", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &result)
	cleaned := result["cleaned"].([]any)
	if len(cleaned) != 1 {
		t.Fatalf("expected 1 cleaned session with --force, got %d", len(cleaned))
	}

	// Verify session dir is removed.
	sessionDir := filepath.Join(dir, ".ap", "runs", "force-sess")
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed after --force clean")
	}
}

func TestCleanNonexistentSession(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	// Idempotent: cleaning a nonexistent session returns success.
	code := runWithDeps([]string{"clean", "ghost", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want success (idempotent)", code)
	}

	var result map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &result)
	cleaned := result["cleaned"].([]any)
	if len(cleaned) != 0 {
		t.Fatalf("expected 0 cleaned sessions, got %d", len(cleaned))
	}
}

func TestCleanAllSessions(t *testing.T) {
	dir := t.TempDir()
	completedAt := "2026-03-04T02:00:00Z"
	setupCleanSession(t, dir, "done-1", &state.SessionState{
		Session:     "done-1",
		Status:      state.StateCompleted,
		StartedAt:   "2026-03-04T00:00:00Z",
		CompletedAt: &completedAt,
		Stages:      []state.StageState{},
		History:     []map[string]any{},
	})
	setupCleanSession(t, dir, "done-2", &state.SessionState{
		Session:     "done-2",
		Status:      state.StateAborted,
		StartedAt:   "2026-03-04T00:00:00Z",
		Stages:      []state.StageState{},
		History:     []map[string]any{},
	})
	setupCleanSession(t, dir, "active", &state.SessionState{
		Session:   "active",
		Status:    state.StateRunning,
		StartedAt: "2026-03-04T00:00:00Z",
		Stages:    []state.StageState{},
		History:   []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "--all", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	cleaned := result["cleaned"].([]any)
	skipped := result["skipped"].([]any)
	if len(cleaned) != 2 {
		t.Fatalf("expected 2 cleaned sessions, got %d", len(cleaned))
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped session, got %d", len(skipped))
	}
}

func TestCleanMissingArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"clean", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestCleanHuman(t *testing.T) {
	dir := t.TempDir()
	completedAt := "2026-03-04T02:00:00Z"
	setupCleanSession(t, dir, "human-clean", &state.SessionState{
		Session:     "human-clean",
		Status:      state.StateCompleted,
		StartedAt:   "2026-03-04T00:00:00Z",
		CompletedAt: &completedAt,
		Stages:      []state.StageState{},
		History:     []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"clean", "human-clean"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	if stdout.String() == "" {
		t.Fatal("expected human output")
	}
}
