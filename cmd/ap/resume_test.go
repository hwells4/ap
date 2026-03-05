package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// setupResumeStore creates an in-memory store with a session in the given status.
func setupResumeStore(t *testing.T, session, status string, iteration, iterCompleted int) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, session, "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	updates := map[string]any{
		"iteration":           iteration,
		"iteration_completed": iterCompleted,
	}
	if status != "running" {
		updates["status"] = status
	}
	if err := s.UpdateSession(ctx, session, updates); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResumePausedSession(t *testing.T) {
	s := setupResumeStore(t, "paused-sess", "paused", 5, 4)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"paused-sess", "--json"}, deps)
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

	// Verify store was updated.
	row, err := s.GetSession(context.Background(), "paused-sess")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != "running" {
		t.Fatalf("status after resume = %q, want %q", row.Status, "running")
	}
}

func TestResumeFailedSession(t *testing.T) {
	s := setupResumeStore(t, "failed-sess", "failed", 3, 2)
	defer s.Close()
	// Set error fields.
	ctx := context.Background()
	_ = s.UpdateSession(ctx, "failed-sess", map[string]any{
		"error":      "provider crash",
		"error_type": "provider_failed",
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"failed-sess", "--json"}, deps)
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

	// Verify store was updated.
	row, err := s.GetSession(ctx, "failed-sess")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != "running" {
		t.Fatalf("status after resume = %q, want %q", row.Status, "running")
	}
}

func TestResumeRunningSession(t *testing.T) {
	s := setupResumeStore(t, "running-sess", "running", 2, 1)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"running-sess", "--json"}, deps)
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
	s := setupResumeStore(t, "done-sess", "completed", 10, 10)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"done-sess", "--json"}, deps)
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
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"ghost", "--json"}, deps)
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

	code := runResume([]string{"--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestResumeWithContextOverride(t *testing.T) {
	s := setupResumeStore(t, "ctx-sess", "paused", 3, 2)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"ctx-sess", "--context", "focus on tests", "--json"}, deps)
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
	s := setupResumeStore(t, "aborted-sess", "aborted", 5, 4)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runResume([]string{"aborted-sess", "--json"}, deps)
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
