package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// setupCleanStore creates a store with a session and a session run dir on disk.
func setupCleanStore(t *testing.T, dir, session, status string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, session, "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		_ = s.UpdateSession(ctx, session, map[string]any{"status": status})
	}
	// Create run dir with dummy files.
	sessionDir := filepath.Join(dir, ".ap", "runs", session)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(`{"type":"session.started"}`+"\n"), 0o644)
	return s
}

func TestCleanCompletedSession(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s := setupCleanStore(t, dir, "done-sess", "completed")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"done-sess", "--json"}, deps)
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
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s := setupCleanStore(t, dir, "fail-sess", "failed")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"fail-sess", "--json"}, deps)
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
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s := setupCleanStore(t, dir, "running-sess", "running")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"running-sess", "--json"}, deps)
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
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s := setupCleanStore(t, dir, "force-sess", "running")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"force-sess", "--force", "--json"}, deps)
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
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"ghost", "--json"}, deps)
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
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Create 3 sessions: 2 terminal, 1 running.
	_ = s.CreateSession(ctx, "done-1", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "done-1", map[string]any{"status": "completed"})
	_ = s.CreateSession(ctx, "done-2", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "done-2", map[string]any{"status": "aborted"})
	_ = s.CreateSession(ctx, "active", "loop", "", "{}")
	// Create run dirs.
	for _, name := range []string{"done-1", "done-2", "active"} {
		sessionDir := filepath.Join(dir, ".ap", "runs", name)
		_ = os.MkdirAll(sessionDir, 0o755)
		_ = os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte("{}"), 0o644)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"--all", "--json"}, deps)
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

	code := runClean([]string{"--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestCleanHuman(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	dir := t.TempDir()
	s := setupCleanStore(t, dir, "human-clean", "completed")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runClean([]string{"human-clean"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	if stdout.String() == "" {
		t.Fatal("expected human output")
	}
}

func TestCleanResolvesSessionFromGlobalIndex(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	projectRoot := t.TempDir()
	s := setupCleanStore(t, projectRoot, "global-clean", "completed")
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runClean([]string{"global-clean", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s; stdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	cleaned, ok := result["cleaned"].([]any)
	if !ok || len(cleaned) != 1 {
		t.Fatalf("expected 1 cleaned session, got: %#v", result["cleaned"])
	}

	sessionDir := filepath.Join(projectRoot, ".ap", "runs", "global-clean")
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed from resolved project root: %v", err)
	}
}

func TestCleanAmbiguousAcrossProjects(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	projectA := t.TempDir()
	a, err := store.Open(filepath.Join(projectA, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateSession(ctx, "dup-clean", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateSession(ctx, "dup-clean", map[string]any{"project_root": projectA}); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	projectB := t.TempDir()
	b, err := store.Open(filepath.Join(projectB, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CreateSession(ctx, "dup-clean", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := b.UpdateSession(ctx, "dup-clean", map[string]any{"project_root": projectB}); err != nil {
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

	code := runClean([]string{"dup-clean", "--json"}, deps)
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
