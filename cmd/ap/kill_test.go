package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

func TestKill_MissingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runKill([]string{}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestKill_NonexistentSession_Idempotent(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runKill([]string{"nonexistent"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (idempotent); stderr: %s", code, output.ExitSuccess, stderr.String())
	}
}

func TestKill_NonexistentSession_JSON(t *testing.T) {
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

	code := runKill([]string{"nonexistent", "--json"}, deps)
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
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "my-session", "loop", "", "{}")

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runKill([]string{"my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitSuccess, stderr.String())
	}

	// Verify state was marked as aborted in the store.
	row, err := s.GetSession(ctx, "my-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.Status != "aborted" {
		t.Errorf("state status = %q, want %q", row.Status, "aborted")
	}
}

func TestKill_SessionWithState_JSON(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "my-session", "loop", "", "{}")

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runKill([]string{"my-session", "--json"}, deps)
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

	code := runKill([]string{"session1", "session2"}, deps)
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

	code := runKill([]string{"--bad-flag", "session"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestKill_CompletedSession_NoChange(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "done-session", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "done-session", map[string]any{"status": "completed"})

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runKill([]string{"done-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	// State should remain completed.
	row, _ := s.GetSession(ctx, "done-session")
	if row.Status != "completed" {
		t.Errorf("status = %q, want %q", row.Status, "completed")
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["was_running"] != false {
		t.Errorf("was_running = %v, want false", result["was_running"])
	}
}

func TestKill_PausedSession_MarksAborted(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "paused-session", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "paused-session", map[string]any{"status": "paused"})

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runKill([]string{"paused-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	row, _ := s.GetSession(ctx, "paused-session")
	if row.Status != "aborted" {
		t.Errorf("status = %q, want %q", row.Status, "aborted")
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["was_running"] != true {
		t.Errorf("was_running = %v, want true", result["was_running"])
	}
	if result["status"] != "killed" {
		t.Errorf("status = %v, want %q", result["status"], "killed")
	}
}
