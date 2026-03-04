package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
)

func TestWriteRunRequest_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run_request.json")

	req := RunRequestFile{
		Session:        "test-session",
		Stage:          "ralph",
		Provider:       "claude",
		Model:          "claude-opus-4-6",
		Iterations:     10,
		PromptTemplate: "You are iteration ${ITERATION}.",
		WorkDir:        "/tmp/project",
		RunDir:         filepath.Join(dir, ".ap", "runs", "test-session"),
	}

	if err := WriteRunRequest(path, req); err != nil {
		t.Fatalf("WriteRunRequest() error: %v", err)
	}

	// Read back and verify.
	loaded, err := ReadRunRequest(path)
	if err != nil {
		t.Fatalf("ReadRunRequest() error: %v", err)
	}

	if loaded.Session != "test-session" {
		t.Errorf("Session = %q, want %q", loaded.Session, "test-session")
	}
	if loaded.Stage != "ralph" {
		t.Errorf("Stage = %q, want %q", loaded.Stage, "ralph")
	}
	if loaded.Provider != "claude" {
		t.Errorf("Provider = %q, want %q", loaded.Provider, "claude")
	}
	if loaded.Iterations != 10 {
		t.Errorf("Iterations = %d, want 10", loaded.Iterations)
	}
	if loaded.PromptTemplate != "You are iteration ${ITERATION}." {
		t.Errorf("PromptTemplate = %q", loaded.PromptTemplate)
	}
	if loaded.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want %q", loaded.Model, "claude-opus-4-6")
	}

	// Verify no .tmp file left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file not cleaned up")
	}
}

func TestWriteRunRequest_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path := filepath.Join(dir, "run_request.json")

	req := RunRequestFile{
		Session:    "test",
		Stage:      "stage",
		Provider:   "claude",
		Iterations: 1,
		RunDir:     dir,
	}

	if err := WriteRunRequest(path, req); err != nil {
		t.Fatalf("WriteRunRequest() error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestReadRunRequest_Validation(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		wantErr string
	}{
		{
			name:    "missing_session",
			payload: map[string]any{"stage": "s", "provider": "p", "iterations": 1, "run_dir": "/d"},
			wantErr: "missing session",
		},
		{
			name:    "missing_stage",
			payload: map[string]any{"session": "s", "provider": "p", "iterations": 1, "run_dir": "/d"},
			wantErr: "missing stage",
		},
		{
			name:    "missing_provider",
			payload: map[string]any{"session": "s", "stage": "st", "iterations": 1, "run_dir": "/d"},
			wantErr: "missing provider",
		},
		{
			name:    "zero_iterations",
			payload: map[string]any{"session": "s", "stage": "st", "provider": "p", "iterations": 0, "run_dir": "/d"},
			wantErr: "iterations must be positive",
		},
		{
			name:    "missing_run_dir",
			payload: map[string]any{"session": "s", "stage": "st", "provider": "p", "iterations": 1},
			wantErr: "missing run_dir",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "req.json")
			data, _ := json.Marshal(tc.payload)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := ReadRunRequest(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !hasSubstring(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestReadRunRequest_FileNotFound(t *testing.T) {
	_, err := ReadRunRequest("/nonexistent/path/request.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadRunRequest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadRunRequest(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestInternalRun_MissingFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no_args", []string{}, "missing required flag --session"},
		{"session_only", []string{"--session", "s"}, "missing required flag --request"},
		{"session_no_value", []string{"--session"}, "flag --session requires a value"},
		{"request_no_value", []string{"--session", "s", "--request"}, "flag --request requires a value"},
		{"unknown_flag", []string{"--bad"}, "unknown flag"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			deps := cliDeps{
				mode:   output.ModeHuman,
				stdout: &bytes.Buffer{},
				stderr: &stderr,
			}

			code := runInternalRun(tc.args, deps)
			if code == output.ExitSuccess {
				t.Fatal("expected non-zero exit code")
			}
			if !hasSubstring(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestInternalRun_SessionMismatch(t *testing.T) {
	dir := t.TempDir()
	reqPath := filepath.Join(dir, "run_request.json")

	req := RunRequestFile{
		Session:    "actual-session",
		Stage:      "ralph",
		Provider:   "claude",
		Iterations: 1,
		RunDir:     filepath.Join(dir, "runs"),
	}
	if err := WriteRunRequest(reqPath, req); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &bytes.Buffer{},
		stderr: &stderr,
	}

	code := runInternalRun([]string{
		"--session", "wrong-session",
		"--request", reqPath,
	}, deps)

	if code == output.ExitSuccess {
		t.Fatal("expected non-zero exit code for session mismatch")
	}
	if !hasSubstring(stderr.String(), "session mismatch") {
		t.Errorf("stderr = %q, want session mismatch error", stderr.String())
	}
}

func TestRunRequestFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.json")

	original := RunRequestFile{
		Session:        "my-session",
		Stage:          "improve",
		Provider:       "claude",
		Model:          "opus",
		Iterations:     25,
		PromptTemplate: "line1\nline2\n${ITERATION}",
		WorkDir:        "/home/user/project",
		Env:            map[string]string{"KEY": "val", "FOO": "bar"},
		RunDir:         filepath.Join(dir, "runs", "my-session"),
		OnEscalate:     "webhook:http://localhost:8123/hooks/escalate",
	}

	if err := WriteRunRequest(path, original); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadRunRequest(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Session != original.Session ||
		loaded.Stage != original.Stage ||
		loaded.Provider != original.Provider ||
		loaded.Model != original.Model ||
		loaded.Iterations != original.Iterations ||
		loaded.PromptTemplate != original.PromptTemplate ||
		loaded.WorkDir != original.WorkDir ||
		loaded.RunDir != original.RunDir ||
		loaded.OnEscalate != original.OnEscalate {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  loaded:   %+v", original, loaded)
	}

	if len(loaded.Env) != 2 || loaded.Env["KEY"] != "val" || loaded.Env["FOO"] != "bar" {
		t.Errorf("env mismatch: %v", loaded.Env)
	}
}

func TestRunRequestFile_JSONSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "req.json")

	req := RunRequestFile{
		Session:    "schema-test",
		Stage:      "ralph",
		Provider:   "claude",
		Iterations: 5,
		RunDir:     "/runs/schema-test",
	}

	if err := WriteRunRequest(path, req); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Verify JSON field names match the contract.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	requiredFields := []string{"session", "stage", "provider", "iterations", "run_dir"}
	for _, field := range requiredFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing required JSON field %q", field)
		}
	}

	// Optional fields should be present if non-zero.
	optionalFields := []string{"model", "prompt_template", "work_dir", "env"}
	for _, field := range optionalFields {
		// These are present but may be zero-valued.
		if _, ok := raw[field]; !ok {
			// Zero-value omission is OK.
			continue
		}
	}
}

func hasSubstring(s, sub string) bool {
	return len(s) >= len(sub) && bytes.Contains([]byte(s), []byte(sub))
}

func TestParseOnEscalateOverride(t *testing.T) {
	t.Run("webhook", func(t *testing.T) {
		handler, err := parseOnEscalateOverride("webhook:https://example.com/hook")
		if err != nil {
			t.Fatalf("parseOnEscalateOverride() error: %v", err)
		}
		if handler.Type != "webhook" || handler.URL != "https://example.com/hook" {
			t.Fatalf("handler = %#v, want webhook URL", handler)
		}
	})

	t.Run("exec", func(t *testing.T) {
		handler, err := parseOnEscalateOverride("exec:notify-send escalation")
		if err != nil {
			t.Fatalf("parseOnEscalateOverride() error: %v", err)
		}
		if handler.Type != "exec" {
			t.Fatalf("handler.Type = %q, want exec", handler.Type)
		}
		if len(handler.Argv) != 2 || handler.Argv[0] != "notify-send" || handler.Argv[1] != "escalation" {
			t.Fatalf("handler.Argv = %#v, want [notify-send escalation]", handler.Argv)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		if _, err := parseOnEscalateOverride("wat"); err == nil {
			t.Fatal("expected parse error")
		}
	})
}
