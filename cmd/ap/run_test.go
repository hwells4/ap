package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
)

func TestRun_MinimalArgs(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	// ap run ralph my-session --fg (foreground so it doesn't try tmux)
	code := runWithDeps([]string{"run", "ralph", "my-session", "--fg"}, deps)
	// With no real provider, we expect a provider error, not an argument error.
	if code == output.ExitInvalidArgs {
		t.Fatalf("got invalid args error, should have parsed successfully; stderr: %s", stderr.String())
	}
}

func TestRun_MissingSpec(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"run"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
	if !containsSubstring(stderr.String(), "INVALID_ARGUMENT") {
		t.Errorf("expected INVALID_ARGUMENT in stderr: %s", stderr.String())
	}
}

func TestRun_MissingSession(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
	if !containsSubstring(stderr.String(), "INVALID_ARGUMENT") {
		t.Errorf("expected INVALID_ARGUMENT in stderr: %s", stderr.String())
	}
}

func TestRun_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"run", "--bogus", "ralph", "sess"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestRun_IterationFlag(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "-n", "25", "--fg"}, deps)
	if code == output.ExitInvalidArgs {
		t.Fatalf("got invalid args error with -n flag; stderr: %s", stderr.String())
	}
}

func TestRun_InvalidIterationCount(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "sess", "-n", "abc"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}
}

func TestRun_ExplainSpec(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}

	// Should have explain_spec at top level.
	if result["explain_spec"] != true {
		t.Error("missing or false explain_spec in output")
	}
	// Should have parsed_spec nested object with kind.
	parsedSpec, ok := result["parsed_spec"].(map[string]any)
	if !ok {
		t.Fatal("missing parsed_spec in explain output")
	}
	if parsedSpec["kind"] != "stage" {
		t.Errorf("parsed_spec.kind = %v, want stage", parsedSpec["kind"])
	}
	if parsedSpec["name"] != "ralph" {
		t.Errorf("parsed_spec.name = %v, want ralph", parsedSpec["name"])
	}
}

func TestRun_ExplainSpec_WithCount(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph:25", "my-session", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	parsedSpec, ok := result["parsed_spec"].(map[string]any)
	if !ok {
		t.Fatal("missing parsed_spec")
	}
	if parsedSpec["iterations"] != float64(25) {
		t.Errorf("parsed_spec.iterations = %v, want 25", parsedSpec["iterations"])
	}
}

func TestRun_ProviderFlag(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "claude", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request, ok := result["request"].(map[string]any)
	if !ok {
		t.Fatal("missing request object")
	}
	if request["provider"] != "claude" {
		t.Errorf("request.provider = %v, want claude", request["provider"])
	}
}

func TestRun_ModelFlag(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "-m", "opus", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request, ok := result["request"].(map[string]any)
	if !ok {
		t.Fatal("missing request object")
	}
	if request["model"] != "opus" {
		t.Errorf("request.model = %v, want opus", request["model"])
	}
}

func TestRun_JSONError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"run", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "INVALID_ARGUMENT" {
		t.Errorf("error code = %v, want INVALID_ARGUMENT", errObj["code"])
	}
}

func TestRun_ForegroundFlag(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--fg", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request, ok := result["request"].(map[string]any)
	if !ok {
		t.Fatal("missing request object")
	}
	if request["foreground"] != true {
		t.Errorf("request.foreground = %v, want true", request["foreground"])
	}
}

// setupStageDir creates a minimal project directory with a ralph stage
// so the spec parser can resolve stage names.
func setupStageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stageDir := filepath.Join(dir, ".claude", "stages", "ralph")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte("name: ralph\ndescription: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte("test prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
