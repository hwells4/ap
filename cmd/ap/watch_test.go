package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// ---------------------------------------------------------------------------
// matchEventType — unit tests
// ---------------------------------------------------------------------------

func TestMatchEventType_DirectMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
		want            bool
	}{
		{"session.completed", "session.completed", true},
		{"signal.escalate", "signal.escalate", true},
		{"session.started", "session.completed", false},
		{"session.idle", "session.idle", true},
		{"iteration.failed", "iteration.failed", true},
		{"", "", true},
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

func TestMatchEventType_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
		want            bool
	}{
		{"Session.Completed", "session.completed", true},
		{"SESSION.COMPLETED", "completed", true},
		{"Signal.Escalate", "ESCALATE", true},
		{"SIGNAL.ESCALATE", "escalated", true},
		{"Session.Failed", "FAILED", true},
		{"SESSION.IDLE", "idle", true},
	}
	for _, tc := range cases {
		if got := matchEventType(tc.actual, tc.pattern); got != tc.want {
			t.Errorf("matchEventType(%q, %q) = %v, want %v", tc.actual, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchEventType_WhitespaceTrimming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
		want            bool
	}{
		{" session.completed ", "session.completed", true},
		{"session.completed", " session.completed ", true},
		{" session.completed ", " completed ", true},
		{" signal.escalate\t", "\tescalate ", true},
	}
	for _, tc := range cases {
		if got := matchEventType(tc.actual, tc.pattern); got != tc.want {
			t.Errorf("matchEventType(%q, %q) = %v, want %v", tc.actual, tc.pattern, got, tc.want)
		}
	}
}

func TestMatchEventType_NoFalsePositives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		actual, pattern string
	}{
		{"session.started", "completed"},
		{"session.started", "escalate"},
		{"session.started", "failed"},
		{"session.started", "idle"},
		{"iteration.completed", "completed"},
		{"some.random.event", "escalate"},
	}
	for _, tc := range cases {
		if got := matchEventType(tc.actual, tc.pattern); got {
			t.Errorf("matchEventType(%q, %q) = true, want false", tc.actual, tc.pattern)
		}
	}
}

// ---------------------------------------------------------------------------
// expandWatchVars — unit tests
// ---------------------------------------------------------------------------

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

func TestExpandWatchVars_MissingData(t *testing.T) {
	t.Parallel()
	// Event with no data or cursor blocks — placeholders remain unexpanded.
	evt := map[string]any{
		"type": "session.started",
	}

	cmd := expandWatchVars("echo ${SESSION} ${EVENT} ${REASON} ${ITERATION}", "sess", evt)
	want := "echo sess session.started ${REASON} ${ITERATION}"
	if cmd != want {
		t.Fatalf("expandWatchVars = %q, want %q", cmd, want)
	}
}

func TestExpandWatchVars_NoPlaceholders(t *testing.T) {
	t.Parallel()
	evt := map[string]any{"type": "session.completed"}
	cmd := expandWatchVars("notify-send finished", "s1", evt)
	if cmd != "notify-send finished" {
		t.Fatalf("expandWatchVars = %q, want unchanged", cmd)
	}
}

func TestExpandWatchVars_MultipleSameVar(t *testing.T) {
	t.Parallel()
	evt := map[string]any{"type": "session.completed"}
	cmd := expandWatchVars("${SESSION}/${SESSION}", "abc", evt)
	if cmd != "abc/abc" {
		t.Fatalf("expandWatchVars = %q, want abc/abc", cmd)
	}
}

func TestExpandWatchVars_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cmd     string
		session string
		evt     map[string]any
		want    string
	}{
		{
			name:    "session var only",
			cmd:     "echo ${SESSION}",
			session: "test-run",
			evt:     map[string]any{"type": "session.started"},
			want:    "echo test-run",
		},
		{
			name:    "event var only",
			cmd:     "echo ${EVENT}",
			session: "s",
			evt:     map[string]any{"type": "signal.escalate"},
			want:    "echo signal.escalate",
		},
		{
			name:    "reason from data",
			cmd:     "log ${REASON}",
			session: "s",
			evt: map[string]any{
				"type": "session.failed",
				"data": map[string]any{"reason": "timeout"},
			},
			want: "log timeout",
		},
		{
			name:    "iteration from cursor",
			cmd:     "iter ${ITERATION}",
			session: "s",
			evt: map[string]any{
				"type":   "iteration.completed",
				"cursor": map[string]any{"iteration": float64(42)},
			},
			want: "iter 42",
		},
		{
			name:    "empty event map",
			cmd:     "${SESSION}",
			session: "x",
			evt:     map[string]any{},
			want:    "x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandWatchVars(tc.cmd, tc.session, tc.evt)
			if got != tc.want {
				t.Errorf("expandWatchVars(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isSessionEndType — unit tests
// ---------------------------------------------------------------------------

func TestIsSessionEndType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		eventType string
		want      bool
	}{
		{"session.completed", true},
		{"session.failed", true},
		{"session.aborted", true},
		{"iteration.completed", false},
		{"session.started", false},
		{"signal.escalate", false},
		{"session.idle", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSessionEndType(tc.eventType); got != tc.want {
			t.Errorf("isSessionEndType(%q) = %v, want %v", tc.eventType, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// parseWatchArgs — unit tests
// ---------------------------------------------------------------------------

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
	session, hooks, errResp := parseWatchArgs([]string{
		"my-session",
		"--on", "completed", "echo done",
		"--on", "escalate", "echo escalated",
	})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if session != "my-session" {
		t.Fatalf("session = %q, want my-session", session)
	}
	if len(hooks) != 2 {
		t.Fatalf("hooks len = %d, want 2", len(hooks))
	}
	if hooks[0].EventType != "completed" || hooks[0].Command != "echo done" {
		t.Errorf("hooks[0] = %+v, want completed/echo done", hooks[0])
	}
	if hooks[1].EventType != "escalate" || hooks[1].Command != "echo escalated" {
		t.Errorf("hooks[1] = %+v, want escalate/echo escalated", hooks[1])
	}
}

func TestParseWatchArgs_ThreeHooks(t *testing.T) {
	t.Parallel()
	_, hooks, errResp := parseWatchArgs([]string{
		"sess",
		"--on", "completed", "cmd1",
		"--on", "failed", "cmd2",
		"--on", "idle", "cmd3",
	})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if len(hooks) != 3 {
		t.Fatalf("hooks len = %d, want 3", len(hooks))
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

func TestParseWatchArgs_OnWithNoArgs(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{"my-session", "--on"})
	if errResp == nil {
		t.Fatal("expected error for --on with no arguments")
	}
}

func TestParseWatchArgs_UnknownFlag(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{"my-session", "--bad-flag"})
	if errResp == nil {
		t.Fatal("expected error for unknown flag")
	}
	if errResp.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("error code = %q, want INVALID_ARGUMENT", errResp.Error.Code)
	}
}

func TestParseWatchArgs_DuplicateSession(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{"session1", "session2"})
	if errResp == nil {
		t.Fatal("expected error for duplicate session")
	}
	if errResp.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("error code = %q, want INVALID_ARGUMENT", errResp.Error.Code)
	}
}

func TestParseWatchArgs_JSONFlagSkipped(t *testing.T) {
	t.Parallel()
	session, hooks, errResp := parseWatchArgs([]string{
		"my-session", "--json", "--on", "completed", "echo done",
	})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if session != "my-session" {
		t.Fatalf("session = %q, want my-session", session)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks len = %d, want 1", len(hooks))
	}
}

func TestParseWatchArgs_EmptyArgs(t *testing.T) {
	t.Parallel()
	_, _, errResp := parseWatchArgs([]string{})
	if errResp == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestParseWatchArgs_SessionBeforeAndAfterOn(t *testing.T) {
	t.Parallel()
	// Session appears before --on — valid.
	session, hooks, errResp := parseWatchArgs([]string{
		"sess", "--on", "escalate", "notify",
	})
	if errResp != nil {
		t.Fatalf("unexpected error: %v", errResp)
	}
	if session != "sess" {
		t.Fatalf("session = %q, want sess", session)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks len = %d, want 1", len(hooks))
	}
}

// ---------------------------------------------------------------------------
// processWatchEvent — unit tests
// ---------------------------------------------------------------------------

func TestProcessWatchEvent_NonMatchingEvent(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
	}
	hooks := []watchHook{{EventType: "completed", Command: "echo done"}}

	// session.started does not match "completed" hook.
	evt := store.EventRow{Type: "session.started", CursorJSON: "{}", DataJSON: "{}"}
	processWatchEvent(evt, "sess", hooks, deps)
	if stdout.Len() > 0 {
		t.Errorf("expected no JSON output for non-matching event, got: %s", stdout.String())
	}
}

func TestProcessWatchEvent_MatchingEvent_JSONOutput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
	}
	// Use "true" as the hook command (produces no output) so that stdout
	// contains only the JSON payload emitted by processWatchEvent.
	hooks := []watchHook{{EventType: "completed", Command: "true"}}

	evt := store.EventRow{Type: "session.completed", CursorJSON: "{}", DataJSON: "{}"}
	processWatchEvent(evt, "my-sess", hooks, deps)

	// stdout contains the JSON line followed by any command output.
	// Parse only the first line (the JSON payload).
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 1 {
		t.Fatalf("expected at least 1 line of output, got none")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
		t.Fatalf("invalid JSON output: %v; raw: %s", err, lines[0])
	}
	if payload["event"] != "session.completed" {
		t.Errorf("event = %v, want session.completed", payload["event"])
	}
	if payload["hook"] != "completed" {
		t.Errorf("hook = %v, want completed", payload["hook"])
	}
	if payload["command"] != "true" {
		t.Errorf("command = %v, want true", payload["command"])
	}
}

func TestProcessWatchEvent_MultipleHooksMatchSameEvent(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
	}
	hooks := []watchHook{
		{EventType: "completed", Command: "cmd1"},
		{EventType: "session.completed", Command: "cmd2"},
	}

	evt := store.EventRow{Type: "session.completed", CursorJSON: "{}", DataJSON: "{}"}
	processWatchEvent(evt, "s", hooks, deps)

	// Both hooks match session.completed: shorthand "completed" and direct "session.completed".
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d: %s", len(lines), stdout.String())
	}
}

func TestProcessWatchEvent_HumanMode_NoJSON(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
	}
	hooks := []watchHook{{EventType: "completed", Command: "true"}}

	evt := store.EventRow{Type: "session.completed", CursorJSON: "{}", DataJSON: "{}"}
	processWatchEvent(evt, "s", hooks, deps)

	// In human mode, no JSON payload is written to stdout (only the command's own output).
	raw := stdout.String()
	if strings.Contains(raw, `"event"`) {
		t.Errorf("human mode should not emit JSON payload, got: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// runWatch — integration tests (store-based)
// ---------------------------------------------------------------------------

// setupWatchStore creates an in-memory store with a session and events.
func setupWatchStore(t *testing.T, session string, events []struct{ eventType, cursorJSON, dataJSON string }) *store.Store {
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

func TestRunWatch_SessionNotFound(t *testing.T) {
	t.Parallel()
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

	code := runWatch([]string{"nonexistent", "--on", "completed", "echo done"}, deps)
	if code != output.ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, output.ExitNotFound)
	}

	// Verify JSON error structure.
	var errPayload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &errPayload); err != nil {
		t.Fatalf("invalid error JSON: %v; raw: %s", err, stdout.String())
	}
	errObj, _ := errPayload["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error object in JSON")
	}
	if errObj["code"] != "SESSION_NOT_FOUND" {
		t.Errorf("error code = %v, want SESSION_NOT_FOUND", errObj["code"])
	}
}

func TestRunWatch_NoHooksError(t *testing.T) {
	// Cannot use t.Parallel() because t.Setenv modifies process environment.
	dir := t.TempDir()

	// Set HOME to temp to avoid loading real config.
	t.Setenv("HOME", dir)

	s := setupWatchStore(t, "sess", nil)
	defer s.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runWatch([]string{"sess"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d; stdout: %s", code, output.ExitInvalidArgs, stdout.String())
	}
}

func TestRunWatch_CompletedEventFiresHook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "test-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.started", "{}", `{"stage":"ralph"}`},
		{"session.completed", "{}", `{"reason":"done"}`},
	})
	defer s.Close()

	// Write a marker file on completed event.
	markerPath := filepath.Join(dir, "hook-fired")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
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

func TestRunWatch_EscalateEventFiresHook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "esc-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"signal.escalate", "{}", `{"reason":"stuck"}`},
		{"session.completed", "{}", "{}"},
	})
	defer s.Close()

	markerPath := filepath.Join(dir, "escalate-fired")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runWatch([]string{"esc-watch", "--on", "escalate", "touch " + markerPath}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, output.ExitSuccess, stdout.String(), stderr.String())
	}

	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("escalate hook did not fire: marker file missing: %v", err)
	}
}

func TestRunWatch_FailedEventFiresHook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "fail-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.failed", "{}", `{"reason":"crash"}`},
	})
	defer s.Close()

	markerPath := filepath.Join(dir, "failed-fired")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runWatch([]string{"fail-watch", "--on", "failed", "touch " + markerPath}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d", code, output.ExitSuccess)
	}

	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("failed hook did not fire: marker file missing: %v", err)
	}
}

func TestRunWatch_MultipleHooksDifferentEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "multi-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"signal.escalate", "{}", `{"reason":"help"}`},
		{"session.completed", "{}", `{"reason":"done"}`},
	})
	defer s.Close()

	escalateMarker := filepath.Join(dir, "escalate-marker")
	completedMarker := filepath.Join(dir, "completed-marker")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	code := runWatch([]string{
		"multi-watch",
		"--on", "escalate", "touch " + escalateMarker,
		"--on", "completed", "touch " + completedMarker,
	}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, output.ExitSuccess, stdout.String(), stderr.String())
	}

	if _, err := os.Stat(escalateMarker); err != nil {
		t.Fatalf("escalate hook did not fire: %v", err)
	}
	if _, err := os.Stat(completedMarker); err != nil {
		t.Fatalf("completed hook did not fire: %v", err)
	}
}

func TestRunWatch_HookReceivesExpandedVars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "var-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.completed", `{"iteration":7}`, `{"reason":"all-done"}`},
	})
	defer s.Close()

	outFile := filepath.Join(dir, "var-output.txt")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	// The hook command writes expanded variables to a file.
	hookCmd := "echo ${SESSION} ${EVENT} ${REASON} ${ITERATION} > " + outFile
	code := runWatch([]string{"var-watch", "--on", "completed", hookCmd}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, output.ExitSuccess, stderr.String())
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	got := strings.TrimSpace(string(content))
	want := "var-watch session.completed all-done 7"
	if got != want {
		t.Fatalf("hook output = %q, want %q", got, want)
	}
}

func TestRunWatch_AbortedEventEndsWatch(t *testing.T) {
	t.Parallel()
	s := setupWatchStore(t, "abort-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.started", "{}", "{}"},
		{"session.aborted", "{}", "{}"},
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

	// Even though hook is on "completed", watch should still exit on session.aborted.
	code := runWatch([]string{"abort-watch", "--on", "completed", "true"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d (session.aborted should end watch)", code, output.ExitSuccess)
	}
}

func TestRunWatch_NonMatchingEventsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := setupWatchStore(t, "skip-watch", []struct{ eventType, cursorJSON, dataJSON string }{
		{"session.started", "{}", "{}"},
		{"iteration.completed", "{}", "{}"},
		{"iteration.started", "{}", "{}"},
		{"session.completed", "{}", "{}"},
	})
	defer s.Close()

	markerPath := filepath.Join(dir, "escalate-not-fired")
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return dir, nil },
		store:  s,
	}

	// Hook on "escalate" — none of the events match escalate.
	code := runWatch([]string{"skip-watch", "--on", "escalate", "touch " + markerPath}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want %d", code, output.ExitSuccess)
	}

	// Marker should NOT exist because no escalate events were emitted.
	if _, err := os.Stat(markerPath); err == nil {
		t.Fatalf("escalate hook should not have fired, but marker file exists")
	}
}

func TestRunWatch_InvalidArgs_ReturnsInvalidArgs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runWatch([]string{}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}
