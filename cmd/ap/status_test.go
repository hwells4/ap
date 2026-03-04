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

// setupStatusSession creates a minimal .ap/runs/{session}/state.json for testing.
func setupStatusSession(t *testing.T, session string, st *state.SessionState) string {
	t.Helper()
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", session)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if err := state.Write(statePath, st); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatusRunningSession(t *testing.T) {
	ts := "2026-03-04T01:00:00Z"
	dir := setupStatusSession(t, "my-session", &state.SessionState{
		Session:            "my-session",
		Type:               "loop",
		Pipeline:           "",
		Status:             state.StateRunning,
		Iteration:          3,
		IterationCompleted: 2,
		IterationStarted:   &ts,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
		EventOffset:        42,
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "my-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	snap, ok := result["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("missing snapshot in response: %#v", result)
	}
	if snap["session"] != "my-session" {
		t.Fatalf("snapshot.session = %v, want my-session", snap["session"])
	}
	if snap["status"] != "running" {
		t.Fatalf("snapshot.status = %v, want running", snap["status"])
	}
	if snap["current_stage"] != "ralph" {
		t.Fatalf("snapshot.current_stage = %v, want ralph", snap["current_stage"])
	}
	// Iteration should be 3 (float64 from JSON).
	if iter, ok := snap["iteration"].(float64); !ok || int(iter) != 3 {
		t.Fatalf("snapshot.iteration = %v, want 3", snap["iteration"])
	}
}

func TestStatusCompletedSession(t *testing.T) {
	completedAt := "2026-03-04T02:00:00Z"
	dir := setupStatusSession(t, "done-session", &state.SessionState{
		Session:            "done-session",
		Type:               "pipeline",
		Pipeline:           "refine.yaml",
		Status:             state.StateCompleted,
		Iteration:          10,
		IterationCompleted: 10,
		StartedAt:          "2026-03-04T00:00:00Z",
		CompletedAt:        &completedAt,
		CurrentStage:       "refine",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
		EventOffset:        100,
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "done-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	snap := result["snapshot"].(map[string]any)
	if snap["status"] != "completed" {
		t.Fatalf("snapshot.status = %v, want completed", snap["status"])
	}
	if snap["completed_at"] != completedAt {
		t.Fatalf("snapshot.completed_at = %v, want %s", snap["completed_at"], completedAt)
	}
}

func TestStatusSessionNotFound(t *testing.T) {
	dir := t.TempDir() // No .ap/runs directory.

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "nonexistent", "--json"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, output.ExitNotFound)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "SESSION_NOT_FOUND" {
		t.Fatalf("error code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}
}

func TestStatusMissingSessionArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"status", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj := result["error"].(map[string]any)
	if errObj["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("error code = %v, want INVALID_ARGUMENT", errObj["code"])
	}
}

func TestStatusExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"status", "my-session", "extra", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestStatusUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"status", "--verbose", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestStatusHumanMode(t *testing.T) {
	ts := "2026-03-04T01:00:00Z"
	dir := setupStatusSession(t, "human-test", &state.SessionState{
		Session:            "human-test",
		Type:               "loop",
		Status:             state.StateRunning,
		Iteration:          2,
		IterationCompleted: 1,
		IterationStarted:   &ts,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "human-test"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected human-readable output, got empty string")
	}
	// Should contain session name and status.
	if !containsAll(out, "human-test", "running") {
		t.Fatalf("human output missing expected content: %s", out)
	}
}

func TestStatusFailedSession(t *testing.T) {
	errMsg := "provider exited with code 1"
	errType := "provider_failed"
	dir := setupStatusSession(t, "failed-session", &state.SessionState{
		Session:            "failed-session",
		Type:               "loop",
		Status:             state.StateFailed,
		Iteration:          5,
		IterationCompleted: 4,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
		Error:              &errMsg,
		ErrorType:          &errType,
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "failed-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	snap := result["snapshot"].(map[string]any)
	if snap["status"] != "failed" {
		t.Fatalf("snapshot.status = %v, want failed", snap["status"])
	}
	if snap["error"] != errMsg {
		t.Fatalf("snapshot.error = %v, want %s", snap["error"], errMsg)
	}
	if snap["error_type"] != errType {
		t.Fatalf("snapshot.error_type = %v, want %s", snap["error_type"], errType)
	}
}

func containsAll(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if !bytes.Contains([]byte(s), []byte(sub)) {
			return false
		}
	}
	return true
}
