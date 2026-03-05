package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/store"
)

func TestIntegration_KillAndResumeLifecycle(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	// Create a shared store for this integration test.
	s, err := store.Open(filepath.Join(root, ".ap", "ap.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Set up a paused session (simulating a runner that escalated).
	killSession := "int-kill"
	if err := s.CreateSession(ctx, killSession, "loop", "", "{}"); err != nil {
		t.Fatalf("create kill session: %v", err)
	}
	_ = s.UpdateSession(ctx, killSession, map[string]any{
		"status":    "paused",
		"iteration": 1,
	})

	var killOut, killErr bytes.Buffer
	killDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &killOut,
		stderr: &killErr,
		getwd:  func() (string, error) { return root, nil },
		store:  s,
	}

	killCode := runKill([]string{killSession, "--json"}, killDeps)
	if killCode != output.ExitSuccess {
		t.Fatalf("kill exit code = %d; stderr: %s", killCode, killErr.String())
	}

	var killPayload map[string]any
	if err := json.Unmarshal(killOut.Bytes(), &killPayload); err != nil {
		t.Fatalf("invalid kill JSON: %v\nraw: %s", err, killOut.String())
	}
	if killPayload["status"] != "killed" {
		t.Fatalf("kill status = %v, want killed", killPayload["status"])
	}

	// Verify store was updated to aborted.
	killRow, err := s.GetSession(ctx, killSession)
	if err != nil {
		t.Fatalf("get killed session: %v", err)
	}
	if killRow.Status != "aborted" {
		t.Fatalf("state after kill = %q, want %q", killRow.Status, "aborted")
	}

	// Set up a failed session for resume with a valid run request.
	resumeSession := "int-resume"
	resumeRunDir := filepath.Join(root, ".ap", "runs", resumeSession)
	resumeReqJSON := testRunRequestJSON(resumeSession, resumeRunDir, root)
	if err := s.CreateSession(ctx, resumeSession, "loop", "", resumeReqJSON); err != nil {
		t.Fatalf("create resume session: %v", err)
	}
	_ = s.UpdateSession(ctx, resumeSession, map[string]any{
		"status":              "failed",
		"iteration":           3,
		"iteration_completed": 2,
		"error":               "timeout",
		"error_type":          "provider_error",
	})

	resumeLauncher := &testLauncher{
		available: true,
		handle:    session.SessionHandle{Session: resumeSession, PID: 55, Backend: "test"},
	}

	var resumeOut, resumeErr bytes.Buffer
	resumeDeps := cliDeps{
		mode:     output.ModeJSON,
		stdout:   &resumeOut,
		stderr:   &resumeErr,
		getwd:    func() (string, error) { return root, nil },
		store:    s,
		launcher: resumeLauncher,
	}

	resumeCode := runResume([]string{resumeSession, "--context", "focus on tests", "--json"}, resumeDeps)
	if resumeCode != output.ExitSuccess {
		t.Fatalf("resume exit code = %d; stderr: %s\nstdout: %s", resumeCode, resumeErr.String(), resumeOut.String())
	}

	var resumePayload map[string]any
	if err := json.Unmarshal(resumeOut.Bytes(), &resumePayload); err != nil {
		t.Fatalf("invalid resume JSON: %v\nraw: %s", err, resumeOut.String())
	}
	if resumePayload["action"] != "resumed" {
		t.Fatalf("resume action = %v, want resumed", resumePayload["action"])
	}
	if resumePayload["status"] != "running" {
		t.Fatalf("resume status = %v, want running", resumePayload["status"])
	}
	if resumePayload["context_override"] != "focus on tests" {
		t.Fatalf("context_override = %v, want focus on tests", resumePayload["context_override"])
	}

	// Verify store was updated to running.
	resumeRow, err := s.GetSession(ctx, resumeSession)
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if resumeRow.Status != "running" {
		t.Fatalf("state after resume = %q, want %q", resumeRow.Status, "running")
	}
}

func TestIntegration_StatusAndLogsJSONContracts(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	s, err := store.Open(filepath.Join(root, ".ap", "ap.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	session := "int-contract"
	if err := s.CreateSession(ctx, session, "loop", "", "{}"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	_ = s.UpdateSession(ctx, session, map[string]any{
		"iteration":           2,
		"iteration_completed": 1,
		"current_stage":       "ralph",
	})

	// Append events to the store.
	if err := s.AppendEvent(ctx, session, "session.started", "{}", `{"stage":"ralph"}`); err != nil {
		t.Fatalf("append event 1: %v", err)
	}
	if err := s.AppendEvent(ctx, session, "iteration.completed", `{"iteration":1}`, `{"decision":"continue"}`); err != nil {
		t.Fatalf("append event 2: %v", err)
	}

	var statusOut, statusErr bytes.Buffer
	statusDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &statusOut,
		stderr: &statusErr,
		getwd:  func() (string, error) { return root, nil },
		store:  s,
	}
	statusCode := runStatus([]string{session, "--json"}, statusDeps)
	if statusCode != output.ExitSuccess {
		t.Fatalf("status exit code = %d; stderr: %s", statusCode, statusErr.String())
	}

	var statusPayload map[string]any
	if err := json.Unmarshal(statusOut.Bytes(), &statusPayload); err != nil {
		t.Fatalf("invalid status JSON: %v\nraw: %s", err, statusOut.String())
	}
	snapshot, ok := statusPayload["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("missing snapshot object: %#v", statusPayload)
	}
	for _, field := range []string{"session", "status", "started_at"} {
		if _, ok := snapshot[field]; !ok {
			t.Fatalf("snapshot missing required field %q: %#v", field, snapshot)
		}
	}

	var logsOut, logsErr bytes.Buffer
	logsDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &logsOut,
		stderr: &logsErr,
		getwd:  func() (string, error) { return root, nil },
		store:  s,
	}
	logsCode := runLogs([]string{session, "--json"}, logsDeps)
	if logsCode != output.ExitSuccess {
		t.Fatalf("logs exit code = %d; stderr: %s", logsCode, logsErr.String())
	}

	lines := strings.Split(strings.TrimSpace(logsOut.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log events, got %d: %s", len(lines), logsOut.String())
	}
	for i, line := range lines {
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
		for _, field := range []string{"type", "session"} {
			if _, ok := evt[field]; !ok {
				t.Fatalf("line %d missing required field %q: %#v", i, field, evt)
			}
		}
	}
}

func TestIntegration_LockContentionAndStalePID(t *testing.T) {
	locksDir := filepath.Join(t.TempDir(), ".ap", "locks")

	held, err := lock.Acquire(locksDir, "contended")
	if err != nil {
		t.Fatalf("acquire initial lock: %v", err)
	}
	defer held.Release()

	if _, err := lock.Acquire(locksDir, "contended"); !errors.Is(err, lock.ErrLocked) {
		t.Fatalf("expected ErrLocked on contention, got %v", err)
	}

	if err := held.Release(); err != nil {
		t.Fatalf("release held lock: %v", err)
	}

	stalePath := filepath.Join(locksDir, "stale.lock")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatalf("mkdir locks dir: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte("999999\n"), 0o644); err != nil {
		t.Fatalf("write stale lock file: %v", err)
	}

	recovered, err := lock.Acquire(locksDir, "stale")
	if err != nil {
		t.Fatalf("acquire stale lock: %v", err)
	}
	defer recovered.Release()
}
