package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// setupLogsStore creates an in-memory store with a session and optional events.
func setupLogsStore(t *testing.T, session string, events []struct{ eventType, cursorJSON, dataJSON string }) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, session, "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	for _, evt := range events {
		if err := s.AppendEvent(ctx, session, evt.eventType, evt.cursorJSON, evt.dataJSON); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestLogsJSON(t *testing.T) {
	s := setupLogsStore(t, "my-logs", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.started", "{}", `{"stage":"ralph"}`},
		{"iteration.started", `{"iteration":1}`, "{}"},
		{"iteration.completed", `{"iteration":1}`, `{"decision":"continue"}`},
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

	code := runLogs([]string{"my-logs", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %s", len(lines), stdout.String())
	}
	for i, line := range lines {
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("line %d: invalid JSON: %v\nline: %s", i, err, line)
		}
		if _, ok := evt["type"]; !ok {
			t.Fatalf("line %d: missing 'type' field", i)
		}
		if _, ok := evt["session"]; !ok {
			t.Fatalf("line %d: missing 'session' field", i)
		}
	}

	// Verify first event type.
	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["type"] != "session.started" {
		t.Fatalf("first event type = %v, want session.started", first["type"])
	}
}

func TestLogsHuman(t *testing.T) {
	s := setupLogsStore(t, "human-logs", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.started", "{}", `{"stage":"ralph"}`},
		{"iteration.completed", `{"iteration":1}`, `{"decision":"continue"}`},
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

	code := runLogs([]string{"human-logs"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected human-readable output")
	}
	if !strings.Contains(out, "session.started") {
		t.Fatalf("human output missing session.started: %s", out)
	}
}

func TestLogsSessionNotFound(t *testing.T) {
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

	code := runLogs([]string{"ghost", "--json"}, deps)
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

func TestLogsMissingArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runLogs([]string{"--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestLogsEmptyEvents(t *testing.T) {
	s := setupLogsStore(t, "empty-logs", nil)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
		store:  s,
	}

	code := runLogs([]string{"empty-logs", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("expected empty output for empty events, got: %s", stdout.String())
	}
}

func TestLogsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runLogs([]string{"--verbose", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestLogsResolvesSessionFromGlobalIndex(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	projectRoot := t.TempDir()
	s, err := store.Open(filepath.Join(projectRoot, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateSession(ctx, "global-logs", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSession(ctx, "global-logs", map[string]any{
		"project_root": projectRoot,
		"status":       "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(ctx, "global-logs", "session.started", "{}", `{"stage":"ralph"}`); err != nil {
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

	code := runLogs([]string{"global-logs", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 event line, got %d: %s", len(lines), stdout.String())
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	if event["session"] != "global-logs" {
		t.Fatalf("event.session = %v, want global-logs", event["session"])
	}
}
