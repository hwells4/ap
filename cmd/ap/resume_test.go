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

func TestResume_MissingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"resume"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestResume_SessionNotFound(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "nonexistent"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitNotFound, stderr.String())
	}
}

func TestResume_SessionNotFound_JSON(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "nonexistent", "--json"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitNotFound, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object in JSON output")
	}
	if errObj["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("error code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}
}

func TestResume_AlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	// state.Init creates state with StateRunning.
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}
}

func TestResume_AlreadyRunning_JSON(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["status"] != "already_running" {
		t.Errorf("status = %v, want already_running", result["status"])
	}
	if result["session"] != "my-session" {
		t.Errorf("session = %v, want my-session", result["session"])
	}
}

func TestResume_CompletedSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.MarkCompleted(statePath); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session"}, deps)
	if code != output.ExitGeneralError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitGeneralError, stderr.String())
	}
}

func TestResume_AbortedSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	// running → paused → aborted
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StatePaused)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StateAborted)
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session"}, deps)
	if code != output.ExitGeneralError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitGeneralError, stderr.String())
	}
}

func TestResume_PausedSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	// running → paused
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StatePaused)
	}); err != nil {
		t.Fatal(err)
	}
	// Write run_request.json so resume can read it.
	reqPath := filepath.Join(sessionDir, "run_request.json")
	if err := WriteRunRequest(reqPath, RunRequestFile{
		Session:    "my-session",
		Stage:      "test-stage",
		Provider:   "claude",
		Iterations: 10,
		WorkDir:    dir,
		RunDir:     sessionDir,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	// Verify state was transitioned to running.
	s, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if s.Status != state.StateRunning {
		t.Errorf("state = %q, want %q", s.Status, state.StateRunning)
	}
}

func TestResume_FailedSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.MarkFailed(statePath, "PROVIDER_ERROR", "timeout"); err != nil {
		t.Fatal(err)
	}
	// Write run_request.json.
	reqPath := filepath.Join(sessionDir, "run_request.json")
	if err := WriteRunRequest(reqPath, RunRequestFile{
		Session:    "my-session",
		Stage:      "test-stage",
		Provider:   "claude",
		Iterations: 10,
		WorkDir:    dir,
		RunDir:     sessionDir,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	// Verify state was transitioned to running.
	s, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if s.Status != state.StateRunning {
		t.Errorf("state = %q, want %q", s.Status, state.StateRunning)
	}
}

func TestResume_PausedSession_JSON(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StatePaused)
	}); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(sessionDir, "run_request.json")
	if err := WriteRunRequest(reqPath, RunRequestFile{
		Session:    "my-session",
		Stage:      "test-stage",
		Provider:   "claude",
		Iterations: 10,
		WorkDir:    dir,
		RunDir:     sessionDir,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["session"] != "my-session" {
		t.Errorf("session = %v, want my-session", result["session"])
	}
	if result["status"] != "resumed" {
		t.Errorf("status = %v, want resumed", result["status"])
	}
}

func TestResume_ExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"resume", "session1", "session2"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestResume_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"resume", "--bad-flag", "session"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestResume_WithContext(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "my-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(sessionDir, "state.json")
	if _, err := state.Init(statePath, "my-session", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StatePaused)
	}); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(sessionDir, "run_request.json")
	if err := WriteRunRequest(reqPath, RunRequestFile{
		Session:    "my-session",
		Stage:      "test-stage",
		Provider:   "claude",
		Iterations: 10,
		WorkDir:    dir,
		RunDir:     sessionDir,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"resume", "my-session", "--context", "override text", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["context_override"] != "override text" {
		t.Errorf("context_override = %v, want 'override text'", result["context_override"])
	}
}
