package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
)

func TestList_HumanMode_ShowsBuiltins(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList(nil, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if out == "" {
		t.Fatal("no output")
	}
	// Built-in stages should appear (ralph is always present).
	if !containsLine(out, "ralph") {
		t.Errorf("expected ralph in output:\n%s", out)
	}
}

func TestList_JSONMode_ParseableOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList([]string{"--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}

	stages, ok := result["stages"]
	if !ok {
		t.Fatal("missing stages key in JSON output")
	}
	stagesSlice, ok := stages.([]any)
	if !ok {
		t.Fatal("stages is not an array")
	}
	if len(stagesSlice) == 0 {
		t.Fatal("stages array is empty")
	}

	// Verify each stage entry has required fields.
	for _, s := range stagesSlice {
		entry, ok := s.(map[string]any)
		if !ok {
			t.Fatal("stage entry is not an object")
		}
		if _, ok := entry["name"]; !ok {
			t.Error("stage entry missing name")
		}
		if _, ok := entry["source"]; !ok {
			t.Error("stage entry missing source")
		}
	}

	// Verify corrections[] is always present.
	corrections, ok := result["corrections"]
	if !ok {
		t.Fatal("missing corrections key")
	}
	if _, ok := corrections.([]any); !ok {
		t.Fatal("corrections is not an array")
	}
}

func TestList_JSONMode_StageMetadata(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList(nil, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	stages := result["stages"].([]any)
	for _, s := range stages {
		entry := s.(map[string]any)
		if entry["name"] == "ralph" {
			if entry["source"] != "builtin" {
				t.Errorf("ralph source = %v, want builtin", entry["source"])
			}
			if desc, ok := entry["description"].(string); !ok || desc == "" {
				t.Errorf("ralph should have a description, got %v", entry["description"])
			}
			return
		}
	}
	t.Fatal("ralph not found in stages")
}

func TestList_DeterministicOrder(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList(nil, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d", code)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	stages := result["stages"].([]any)
	names := make([]string, len(stages))
	for i, s := range stages {
		names[i] = s.(map[string]any)["name"].(string)
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("stages not sorted: %q comes after %q", names[i], names[i-1])
		}
	}
}

func TestList_ProjectStages_Override(t *testing.T) {
	dir := t.TempDir()
	stageDir := filepath.Join(dir, ".claude", "stages", "ralph")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte("description: custom ralph\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runList(nil, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	stages := result["stages"].([]any)
	for _, s := range stages {
		entry := s.(map[string]any)
		if entry["name"] == "ralph" {
			if entry["source"] != "project" {
				t.Errorf("ralph source = %v, want project", entry["source"])
			}
			if entry["description"] != "custom ralph" {
				t.Errorf("ralph description = %v, want 'custom ralph'", entry["description"])
			}
			return
		}
	}
	t.Fatal("ralph not found in stages")
}

func TestList_ProjectStages_Alongside(t *testing.T) {
	dir := t.TempDir()
	stageDir := filepath.Join(dir, ".claude", "stages", "my-custom-stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte("description: my custom stage\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runList(nil, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	stages := result["stages"].([]any)
	foundCustom := false
	foundBuiltin := false
	for _, s := range stages {
		entry := s.(map[string]any)
		if entry["name"] == "my-custom-stage" && entry["source"] == "project" {
			foundCustom = true
		}
		if entry["name"] == "ralph" && entry["source"] == "builtin" {
			foundBuiltin = true
		}
	}
	if !foundCustom {
		t.Error("custom project stage not found")
	}
	if !foundBuiltin {
		t.Error("builtin ralph not found alongside custom stage")
	}
}

func TestList_UnknownFlag_Human(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList([]string{"--verbose"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
	errOut := stderr.String()
	if !containsSubstring(errOut, "UNKNOWN_FLAG") {
		t.Errorf("expected UNKNOWN_FLAG in stderr: %s", errOut)
	}
}

func TestList_UnknownFlag_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList([]string{"--verbose"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object in JSON output")
	}
	if errObj["code"] != "UNKNOWN_FLAG" {
		t.Errorf("error code = %v, want UNKNOWN_FLAG", errObj["code"])
	}
}

func TestList_UnexpectedArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runList([]string{"something"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

// containsLine checks if any line of out contains substr.
func containsLine(out, substr string) bool {
	for _, line := range splitLines(out) {
		if containsSubstring(line, substr) {
			return true
		}
	}
	return false
}

func containsSubstring(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range bytes.Split([]byte(s), []byte("\n")) {
		lines = append(lines, string(line))
	}
	return lines
}
