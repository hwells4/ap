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

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runner"
	"github.com/hwells4/ap/internal/state"
)

func TestIntegration_RunKillAndResumeLifecycle(t *testing.T) {
	root := t.TempDir()

	// "run" step with mock provider: escalate should pause the session.
	killSession := "int-kill"
	killRunDir := filepath.Join(root, ".ap", "runs", killSession)
	if err := os.MkdirAll(killRunDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need input", "human", "choose", []string{"A", "B"}),
		),
	)
	runResult, err := runner.Run(context.Background(), runner.Config{
		Session:        killSession,
		RunDir:         killRunDir,
		StageName:      "ralph",
		Provider:       prov,
		Iterations:     3,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        root,
	})
	if err != nil {
		t.Fatalf("runner.Run() error: %v", err)
	}
	if runResult.Status != state.StatePaused {
		t.Fatalf("run status = %q, want %q", runResult.Status, state.StatePaused)
	}

	var killOut, killErr bytes.Buffer
	killDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &killOut,
		stderr: &killErr,
		getwd:  func() (string, error) { return root, nil },
	}

	killCode := runWithDeps([]string{"kill", killSession, "--json"}, killDeps)
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

	killedState, err := state.Load(filepath.Join(killRunDir, "state.json"))
	if err != nil {
		t.Fatalf("load killed state: %v", err)
	}
	if killedState.Status != state.StateAborted {
		t.Fatalf("state after kill = %q, want %q", killedState.Status, state.StateAborted)
	}

	// Separate resumable session in failed state.
	resumeSession := "int-resume"
	resumeRunDir := filepath.Join(root, ".ap", "runs", resumeSession)
	if err := os.MkdirAll(resumeRunDir, 0o755); err != nil {
		t.Fatalf("mkdir resume dir: %v", err)
	}
	resumeStatePath := filepath.Join(resumeRunDir, "state.json")
	if _, err := state.Init(resumeStatePath, resumeSession, "loop", ""); err != nil {
		t.Fatalf("init resume state: %v", err)
	}
	if _, err := state.MarkFailed(resumeStatePath, "provider_error", "timeout"); err != nil {
		t.Fatalf("mark resume state failed: %v", err)
	}
	if err := WriteRunRequest(filepath.Join(resumeRunDir, "run_request.json"), RunRequestFile{
		Session:    resumeSession,
		Stage:      "ralph",
		Provider:   "claude",
		Iterations: 5,
		WorkDir:    root,
		RunDir:     resumeRunDir,
	}); err != nil {
		t.Fatalf("write run_request: %v", err)
	}

	var resumeOut, resumeErr bytes.Buffer
	resumeDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &resumeOut,
		stderr: &resumeErr,
		getwd:  func() (string, error) { return root, nil },
	}

	resumeCode := runWithDeps([]string{"resume", resumeSession, "--context", "focus on tests", "--json"}, resumeDeps)
	if resumeCode != output.ExitSuccess {
		t.Fatalf("resume exit code = %d; stderr: %s", resumeCode, resumeErr.String())
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

	resumedState, err := state.Load(resumeStatePath)
	if err != nil {
		t.Fatalf("load resumed state: %v", err)
	}
	if resumedState.Status != state.StateRunning {
		t.Fatalf("state after resume = %q, want %q", resumedState.Status, state.StateRunning)
	}
}

func TestIntegration_StatusAndLogsJSONContracts(t *testing.T) {
	root := t.TempDir()
	session := "int-contract"
	sessionDir := filepath.Join(root, ".ap", "runs", session)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}

	started := "2026-03-04T01:00:00Z"
	if err := state.Write(filepath.Join(sessionDir, "state.json"), &state.SessionState{
		Session:            session,
		Type:               "loop",
		Status:             state.StateRunning,
		Iteration:          2,
		IterationCompleted: 1,
		IterationStarted:   &started,
		StartedAt:          "2026-03-04T00:00:00Z",
		CurrentStage:       "ralph",
		Stages:             []state.StageState{},
		History:            []map[string]any{},
	}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	if err := events.Append(eventsPath, events.NewEvent(events.TypeSessionStart, session, nil, map[string]any{"stage": "ralph"})); err != nil {
		t.Fatalf("append event 1: %v", err)
	}
	if err := events.Append(eventsPath, events.NewEvent(events.TypeIterationComplete, session, &events.Cursor{Iteration: 1}, map[string]any{"decision": "continue"})); err != nil {
		t.Fatalf("append event 2: %v", err)
	}

	var statusOut, statusErr bytes.Buffer
	statusDeps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &statusOut,
		stderr: &statusErr,
		getwd:  func() (string, error) { return root, nil },
	}
	statusCode := runWithDeps([]string{"status", session, "--json"}, statusDeps)
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
	for _, field := range []string{"session", "status", "iteration", "started_at", "current_stage"} {
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
	}
	logsCode := runWithDeps([]string{"logs", session, "--json"}, logsDeps)
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
		for _, field := range []string{"ts", "type", "session"} {
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
