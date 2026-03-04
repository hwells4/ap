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

// setupResumeSession creates .ap/runs/{session}/ with state.json and run_request.json.
func setupResumeSession(t *testing.T, session string, st *state.SessionState) string {
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
	// Write a minimal run_request.json.
	reqPath := filepath.Join(sessionDir, "run_request.json")
	reqData := RunRequestFile{
		Session:        session,
		Stage:          "ralph",
		Provider:       "claude",
		Model:          "",
		Iterations:     10,
		PromptTemplate: "Run iteration ${ITERATION}",
		WorkDir:        dir,
		RunDir:         sessionDir,
	}
	if err := WriteRunRequest(reqPath, reqData); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResumePausedSession(t *testing.T) {
	dir := setupResumeSession(t, "paused-sess", &state.SessionState{
		Session:            "paused-sess",
		Type:               "loop",
		Status:             state.StatePaused,
		Iteration:          5,
		IterationCompleted: 4,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "paused-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	if result["session"] != "paused-sess" {
		t.Fatalf("session = %v, want paused-sess", result["session"])
	}
	if result["action"] != "resumed" {
		t.Fatalf("action = %v, want resumed", result["action"])
	}
	resumeFrom, ok := result["resume_from"].(float64)
	if !ok || int(resumeFrom) != 5 {
		t.Fatalf("resume_from = %v, want 5", result["resume_from"])
	}
}

func TestResumeFailedSession(t *testing.T) {
	errMsg := "provider crash"
	errType := "provider_failed"
	dir := setupResumeSession(t, "failed-sess", &state.SessionState{
		Session:            "failed-sess",
		Type:               "loop",
		Status:             state.StateFailed,
		Iteration:          3,
		IterationCompleted: 2,
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

	code := runWithDeps([]string{"resume", "failed-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result["action"] != "resumed" {
		t.Fatalf("action = %v, want resumed", result["action"])
	}
	resumeFrom, ok := result["resume_from"].(float64)
	if !ok || int(resumeFrom) != 3 {
		t.Fatalf("resume_from = %v, want 3", result["resume_from"])
	}
}

func TestResumeRunningSession(t *testing.T) {
	dir := setupResumeSession(t, "running-sess", &state.SessionState{
		Session:            "running-sess",
		Type:               "loop",
		Status:             state.StateRunning,
		Iteration:          2,
		IterationCompleted: 1,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "running-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result["action"] != "already_running" {
		t.Fatalf("action = %v, want already_running", result["action"])
	}
}

func TestResumeCompletedSession(t *testing.T) {
	completedAt := "2026-03-04T02:00:00Z"
	dir := setupResumeSession(t, "done-sess", &state.SessionState{
		Session:            "done-sess",
		Type:               "loop",
		Status:             state.StateCompleted,
		Iteration:          10,
		IterationCompleted: 10,
		StartedAt:          "2026-03-04T00:00:00Z",
		CompletedAt:        &completedAt,
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "done-sess", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d; stdout: %s", code, output.ExitInvalidArgs, stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj := result["error"].(map[string]any)
	if errObj["code"] != "SESSION_COMPLETED" {
		t.Fatalf("error code = %v, want SESSION_COMPLETED", errObj["code"])
	}
}

func TestResumeSessionNotFound(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "ghost", "--json"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, output.ExitNotFound)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj := result["error"].(map[string]any)
	if errObj["code"] != "SESSION_NOT_FOUND" {
		t.Fatalf("error code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}
}

func TestResumeMissingSessionArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"resume", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestResumeWithContextOverride(t *testing.T) {
	dir := setupResumeSession(t, "ctx-sess", &state.SessionState{
		Session:            "ctx-sess",
		Type:               "loop",
		Status:             state.StatePaused,
		Iteration:          3,
		IterationCompleted: 2,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "ctx-sess", "--context", "focus on tests", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s\nstdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	if result["action"] != "resumed" {
		t.Fatalf("action = %v, want resumed", result["action"])
	}
	if result["context_override"] != "focus on tests" {
		t.Fatalf("context_override = %v, want 'focus on tests'", result["context_override"])
	}
}

func TestResumeAbortedSession(t *testing.T) {
	dir := setupResumeSession(t, "aborted-sess", &state.SessionState{
		Session:            "aborted-sess",
		Type:               "loop",
		Status:             state.StateAborted,
		Iteration:          5,
		IterationCompleted: 4,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "aborted-sess", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj := result["error"].(map[string]any)
	if errObj["code"] != "SESSION_ABORTED" {
		t.Fatalf("error code = %v, want SESSION_ABORTED", errObj["code"])
	}
}
