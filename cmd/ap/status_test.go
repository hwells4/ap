package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// setupStatusStore creates an in-memory store with a session for testing.
func setupStatusStore(t *testing.T, name string, updates map[string]any) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, name, "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if len(updates) > 0 {
		if err := s.UpdateSession(ctx, name, updates); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestStatusRunningSession(t *testing.T) {
	s := setupStatusStore(t, "my-session", map[string]any{
		"current_stage":       "ralph",
		"iteration":           3,
		"iteration_completed": 2,
	})
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runStatus([]string{"my-session", "--json"}, deps)
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
	if iter, ok := snap["iteration"].(float64); !ok || int(iter) != 3 {
		t.Fatalf("snapshot.iteration = %v, want 3", snap["iteration"])
	}
}

func TestStatusCompletedSession(t *testing.T) {
	completedAt := "2026-03-04T02:00:00Z"
	s := setupStatusStore(t, "done-session", map[string]any{
		"status":              "completed",
		"iteration":           10,
		"iteration_completed": 10,
		"completed_at":        completedAt,
		"current_stage":       "refine",
	})
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runStatus([]string{"done-session", "--json"}, deps)
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

	code := runStatus([]string{"nonexistent", "--json"}, deps)
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

	code := runStatus([]string{"--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
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

	code := runStatus([]string{"my-session", "extra", "--json"}, deps)
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

	code := runStatus([]string{"--verbose", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestStatusResolvesSessionFromGlobalIndex(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	projectRoot := t.TempDir()
	s, err := store.Open(filepath.Join(projectRoot, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, "global-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSession(ctx, "global-session", map[string]any{
		"project_root": projectRoot,
		"status":       "running",
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runStatus([]string{"global-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	snap := result["snapshot"].(map[string]any)
	if snap["session"] != "global-session" {
		t.Fatalf("snapshot.session = %v, want global-session", snap["session"])
	}
}

func TestStatusAmbiguousAcrossProjects(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	projectA := t.TempDir()
	a, err := store.Open(filepath.Join(projectA, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateSession(ctx, "dup-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateSession(ctx, "dup-session", map[string]any{"project_root": projectA}); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	projectB := t.TempDir()
	b, err := store.Open(filepath.Join(projectB, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CreateSession(ctx, "dup-session", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := b.UpdateSession(ctx, "dup-session", map[string]any{"project_root": projectB}); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runStatus([]string{"dup-session", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, output.ExitInvalidArgs, stdout.String(), stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	errObj := result["error"].(map[string]any)
	if errObj["code"] != "SESSION_AMBIGUOUS" {
		t.Fatalf("error.code = %v, want SESSION_AMBIGUOUS", errObj["code"])
	}
}

func TestStatusHumanMode(t *testing.T) {
	s := setupStatusStore(t, "human-test", map[string]any{
		"current_stage":       "ralph",
		"iteration":           2,
		"iteration_completed": 1,
	})
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runStatus([]string{"human-test"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected human-readable output, got empty string")
	}
	if !containsAll(out, "human-test", "running") {
		t.Fatalf("human output missing expected content: %s", out)
	}
}

func TestStatusHumanMode_ShowsPipelineStageProgress(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "pipeline-human", "pipeline", "refine", "{}")
	_ = s.UpdateSession(ctx, "pipeline-human", map[string]any{
		"node_id":             "refine-tasks",
		"iteration":           2,
		"iteration_completed": 1,
		"current_stage":       "refine-tasks",
		"stages_json":         `[{"name":"improve-plan","index":0,"iterations":3},{"name":"refine-tasks","index":1,"iterations":5}]`,
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runStatus([]string{"pipeline-human"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !containsAll(out, "Pipeline:   stage 2 of 2", "Node:       refine-tasks") {
		t.Fatalf("pipeline progress missing from human output: %s", out)
	}
}

func TestStatusFailedSession(t *testing.T) {
	errMsg := "provider exited with code 1"
	errType := "provider_failed"
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_ = s.CreateSession(ctx, "failed-session", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "failed-session", map[string]any{
		"status":              "failed",
		"iteration":           5,
		"iteration_completed": 4,
		"current_stage":       "ralph",
		"error":               errMsg,
		"error_type":          errType,
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runStatus([]string{"failed-session", "--json"}, deps)
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
