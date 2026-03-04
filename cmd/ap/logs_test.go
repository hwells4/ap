package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/output"
)

// setupLogsSession creates .ap/runs/{session}/events.jsonl with the given events.
func setupLogsSession(t *testing.T, session string, evts []events.Event) string {
	t.Helper()
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", session)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	for _, evt := range evts {
		if err := events.Append(eventsPath, evt); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLogsJSON(t *testing.T) {
	dir := setupLogsSession(t, "my-logs", []events.Event{
		events.NewEvent(events.TypeSessionStart, "my-logs", nil, map[string]any{"stage": "ralph"}),
		events.NewEvent(events.TypeIterationStart, "my-logs", &events.Cursor{Iteration: 1}, nil),
		events.NewEvent(events.TypeIterationComplete, "my-logs", &events.Cursor{Iteration: 1}, map[string]any{"decision": "continue"}),
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"logs", "my-logs", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	// Each line should be valid JSON (raw JSONL passthrough).
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

	// Verify event types in order.
	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["type"] != events.TypeSessionStart {
		t.Fatalf("first event type = %v, want %s", first["type"], events.TypeSessionStart)
	}
}

func TestLogsHuman(t *testing.T) {
	dir := setupLogsSession(t, "human-logs", []events.Event{
		events.NewEvent(events.TypeSessionStart, "human-logs", nil, map[string]any{"stage": "ralph"}),
		events.NewEvent(events.TypeIterationComplete, "human-logs", &events.Cursor{Iteration: 1}, map[string]any{"decision": "continue"}),
	})

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"logs", "human-logs"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("expected human-readable output")
	}
	// Should contain event type and session.
	if !strings.Contains(out, "session.started") {
		t.Fatalf("human output missing session.started: %s", out)
	}
}

func TestLogsSessionNotFound(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"logs", "ghost", "--json"}, deps)
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

	code := runWithDeps([]string{"logs", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestLogsEmptyEventsFile(t *testing.T) {
	dir := setupLogsSession(t, "empty-logs", nil)

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"logs", "empty-logs", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	// Empty file → no output.
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

	code := runWithDeps([]string{"logs", "--verbose", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}
