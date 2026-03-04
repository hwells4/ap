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

func TestKill_MissingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"kill"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestKill_NonexistentSession_Idempotent(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "nonexistent"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (idempotent); stderr: %s", code, output.ExitSuccess, stderr.String())
	}
}

func TestKill_NonexistentSession_JSON(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "nonexistent", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}

	if result["session"] != "nonexistent" {
		t.Errorf("session = %v, want nonexistent", result["session"])
	}
	if result["was_running"] != false {
		t.Errorf("was_running = %v, want false", result["was_running"])
	}
}

func TestKill_SessionWithState_MarksAborted(t *testing.T) {
	dir := t.TempDir()

	// Create a session directory with state.json in running state.
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
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	// Verify state was marked as aborted.
	s, err := state.Load(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if s.Status != state.StateAborted {
		t.Errorf("state status = %q, want %q", s.Status, state.StateAborted)
	}
}

func TestKill_SessionWithState_JSON(t *testing.T) {
	dir := t.TempDir()

	// Create a session directory with state.json.
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

	code := runWithDeps([]string{"kill", "my-session", "--json"}, deps)
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
	if result["status"] != "killed" {
		t.Errorf("status = %v, want killed", result["status"])
	}
}

func TestKill_ExtraArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"kill", "session1", "session2"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestKill_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWithDeps([]string{"kill", "--bad-flag", "session"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}
