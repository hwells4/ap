package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
)

func TestMatchEventType_DirectMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
		want            bool
	}{
		{"session.completed", "session.completed", true},
		{"signal.escalate", "signal.escalate", true},
		{"session.started", "session.completed", false},
	}
	for _, tc := range cases {
		if got := matchEventType(tc.actual, tc.pattern); got != tc.want {
			t.Errorf("matchEventType(%q, %q) = %v, want %v", tc.actual, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchEventType_Shorthands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
		want            bool
	}{
		{"session.completed", "completed", true},
		{"signal.escalate", "escalate", true},
		{"signal.escalate", "escalated", true},
		{"iteration.failed", "failed", true},
		{"session.failed", "failed", true},
		{"session.idle", "idle", true},
		{"session.started", "completed", false},
	}
	for _, tc := range cases {
		if got := matchEventType(tc.actual, tc.pattern); got != tc.want {
			t.Errorf("matchEventType(%q, %q) = %v, want %v", tc.actual, tc.pattern, got, tc.want)
		}
	}
}

func TestExpandWatchVars(t *testing.T) {
	t.Parallel()
	evt := map[string]any{
		"type": "session.completed",
		"data": map[string]any{
			"reason": "all done",
		},
		"cursor": map[string]any{
			"iteration": float64(5),
		},
	}

	cmd := expandWatchVars("echo ${SESSION} ${EVENT} ${REASON} ${ITERATION}", "my-sess", evt)
	want := "echo my-sess session.completed all done 5"
	if cmd != want {
		t.Fatalf("expandWatchVars = %q, want %q", cmd, want)
	}
}

func TestIsSessionEnd(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want bool
	}{
		{`{"type":"session.completed"}`, true},
		{`{"type":"session.failed"}`, true},
		{`{"type":"session.aborted"}`, true},
		{`{"type":"iteration.completed"}`, false},
		{`{"type":"session.started"}`, false},
		{`invalid json`, false},
	}
	for _, tc := range cases {
		if got := isSessionEnd(tc.line); got != tc.want {
			t.Errorf("isSessionEnd(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestParseWatchArgs_Basic(t *testing.T) {
	t.Parallel()
	session, hooks, errResp := parseWatchArgs([]string{"my-session", "--on", "completed", "echo done"})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if session != "my-session" {
		t.Fatalf("session = %q, want my-session", session)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks len = %d, want 1", len(hooks))
	}
	if hooks[0].EventType != "completed" {
		t.Fatalf("hook event = %q, want completed", hooks[0].EventType)
	}
	if hooks[0].Command != "echo done" {
		t.Fatalf("hook command = %q, want 'echo done'", hooks[0].Command)
	}
}

func TestParseWatchArgs_MultipleHooks(t *testing.T) {
	t.Parallel()
	_, hooks, errResp := parseWatchArgs([]string{
		"my-session",
		"--on", "completed", "echo done",
		"--on", "escalate", "echo escalated",
	})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if len(hooks) != 2 {
		t.Fatalf("hooks len = %d, want 2", len(hooks))
	}
}

func TestParseWatchArgs_MissingSession(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{"--on", "completed", "echo done"})
	if errResp == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestParseWatchArgs_IncompleteOn(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{"my-session", "--on", "completed"})
	if errResp == nil {
		t.Fatal("expected error for incomplete --on")
	}
}

func TestRunWatch_SessionNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWatch([]string{"nonexistent", "--on", "completed", "echo done"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, output.ExitNotFound)
	}
}

func TestRunWatch_CompletedEventFiresHook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, ".ap", "runs", "test-watch")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write an events file that ends with session.completed.
	events := []map[string]any{
		{"type": "session.started", "ts": "2026-03-04T00:00:00Z", "session": "test-watch", "data": map[string]any{}},
		{"type": "session.completed", "ts": "2026-03-04T00:01:00Z", "session": "test-watch", "data": map[string]any{"reason": "done"}},
	}
	var eventsData bytes.Buffer
	for _, evt := range events {
		line, _ := json.Marshal(evt)
		eventsData.Write(line)
		eventsData.WriteByte('\n')
	}
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, eventsData.Bytes(), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	// Write a marker file on completed event.
	markerPath := filepath.Join(dir, "hook-fired")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWatch([]string{"test-watch", "--on", "completed", "touch " + markerPath}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, output.ExitSuccess, stdout.String(), stderr.String())
	}

	// Verify the hook was fired.
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("hook command did not fire: marker file missing: %v", err)
	}
}
