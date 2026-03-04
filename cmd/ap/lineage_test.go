package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/state"
)

func TestStatusShowsParentSession(t *testing.T) {
	dir := setupStatusSession(t, "child-sess", &state.SessionState{
		Session:       "child-sess",
		Type:          "loop",
		Status:        state.StateRunning,
		StartedAt:     "2026-03-04T00:00:00Z",
		Stages:        []state.StageState{},
		History:       []map[string]any{},
		ParentSession: "parent-sess",
	})

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "child-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	snap := result["snapshot"].(map[string]any)
	if snap["parent_session"] != "parent-sess" {
		t.Errorf("parent_session = %v, want parent-sess", snap["parent_session"])
	}
}

func TestStatusShowsChildSessions(t *testing.T) {
	dir := setupStatusSession(t, "parent-sess", &state.SessionState{
		Session:       "parent-sess",
		Type:          "loop",
		Status:        state.StateRunning,
		StartedAt:     "2026-03-04T00:00:00Z",
		Stages:        []state.StageState{},
		History:       []map[string]any{},
		ChildSessions: []string{"child-1", "child-2"},
	})

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "parent-sess", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	snap := result["snapshot"].(map[string]any)
	children, ok := snap["child_sessions"].([]any)
	if !ok {
		t.Fatalf("child_sessions missing or wrong type: %T", snap["child_sessions"])
	}
	if len(children) != 2 {
		t.Fatalf("child_sessions len = %d, want 2", len(children))
	}
	if children[0] != "child-1" || children[1] != "child-2" {
		t.Errorf("child_sessions = %v, want [child-1, child-2]", children)
	}
}

func TestStatusHumanShowsLineage(t *testing.T) {
	dir := setupStatusSession(t, "lineage-test", &state.SessionState{
		Session:       "lineage-test",
		Type:          "loop",
		Status:        state.StateRunning,
		StartedAt:     "2026-03-04T00:00:00Z",
		Stages:        []state.StageState{},
		History:       []map[string]any{},
		ParentSession: "my-parent",
		ChildSessions: []string{"child-a", "child-b"},
	})

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"status", "lineage-test"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	out := stdout.String()
	if !strings.Contains(out, "Parent:") {
		t.Errorf("human output missing Parent line: %s", out)
	}
	if !strings.Contains(out, "my-parent") {
		t.Errorf("human output missing parent name: %s", out)
	}
	if !strings.Contains(out, "Children:") {
		t.Errorf("human output missing Children line: %s", out)
	}
	if !strings.Contains(out, "child-a") {
		t.Errorf("human output missing child-a: %s", out)
	}
}

func TestKillCascadesToChildren(t *testing.T) {
	dir := t.TempDir()

	parentDir := filepath.Join(dir, ".ap", "runs", "parent-sess")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentState := filepath.Join(parentDir, "state.json")
	if _, err := state.Init(parentState, "parent-sess", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(parentState, func(s *state.SessionState) error {
		s.ChildSessions = []string{"child-1", "child-2"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	child1Dir := filepath.Join(dir, ".ap", "runs", "child-1")
	if err := os.MkdirAll(child1Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	child1State := filepath.Join(child1Dir, "state.json")
	if _, err := state.Init(child1State, "child-1", "loop", ""); err != nil {
		t.Fatal(err)
	}

	child2Dir := filepath.Join(dir, ".ap", "runs", "child-2")
	if err := os.MkdirAll(child2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	child2State := filepath.Join(child2Dir, "state.json")
	if _, err := state.Init(child2State, "child-2", "loop", ""); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "parent-sess"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	for _, child := range []string{"child-1", "child-2"} {
		s, err := state.Load(filepath.Join(dir, ".ap", "runs", child, "state.json"))
		if err != nil {
			t.Fatalf("load %s state: %v", child, err)
		}
		if s.Status != state.StateAborted {
			t.Errorf("%s status = %q, want %q", child, s.Status, state.StateAborted)
		}
	}
}

func TestKillCascadeResponse(t *testing.T) {
	dir := t.TempDir()

	parentDir := filepath.Join(dir, ".ap", "runs", "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentState := filepath.Join(parentDir, "state.json")
	if _, err := state.Init(parentState, "parent", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(parentState, func(s *state.SessionState) error {
		s.ChildSessions = []string{"running-child"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	childDir := filepath.Join(dir, ".ap", "runs", "running-child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	childState := filepath.Join(childDir, "state.json")
	if _, err := state.Init(childState, "running-child", "loop", ""); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "parent", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}

	children, ok := result["children_killed"].([]any)
	if !ok {
		t.Fatalf("children_killed missing or wrong type: %T", result["children_killed"])
	}
	if len(children) != 1 || children[0] != "running-child" {
		t.Errorf("children_killed = %v, want [running-child]", children)
	}
}

func TestKillSkipsTerminalChildren(t *testing.T) {
	dir := t.TempDir()

	parentDir := filepath.Join(dir, ".ap", "runs", "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentState := filepath.Join(parentDir, "state.json")
	if _, err := state.Init(parentState, "parent", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Update(parentState, func(s *state.SessionState) error {
		s.ChildSessions = []string{"done-child", "live-child"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	doneDir := filepath.Join(dir, ".ap", "runs", "done-child")
	if err := os.MkdirAll(doneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doneState := filepath.Join(doneDir, "state.json")
	if _, err := state.Init(doneState, "done-child", "loop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := state.MarkCompleted(doneState); err != nil {
		t.Fatal(err)
	}

	liveDir := filepath.Join(dir, ".ap", "runs", "live-child")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	liveState := filepath.Join(liveDir, "state.json")
	if _, err := state.Init(liveState, "live-child", "loop", ""); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		getwd:  func() (string, error) { return dir, nil },
	}

	code := runWithDeps([]string{"kill", "parent", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	children, ok := result["children_killed"].([]any)
	if !ok {
		t.Fatalf("children_killed type = %T", result["children_killed"])
	}
	if len(children) != 1 || children[0] != "live-child" {
		t.Errorf("children_killed = %v, want [live-child]", children)
	}

	s, err := state.Load(doneState)
	if err != nil {
		t.Fatalf("load done-child: %v", err)
	}
	if s.Status != state.StateCompleted {
		t.Errorf("done-child status = %q, want %q", s.Status, state.StateCompleted)
	}
}
