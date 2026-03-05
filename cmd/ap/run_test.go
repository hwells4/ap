package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/store"
)

func TestRun_MinimalArgs(t *testing.T) {
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

	// Use --explain-spec to validate arg parsing without launching a session.
	code := runWithDeps([]string{"run", "ralph", "my-session", "--explain-spec", "--json"}, deps)
	if code == output.ExitInvalidArgs {
		t.Fatalf("got invalid args error, should have parsed successfully; stderr: %s", stderr.String())
	}
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
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
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	// Use --explain-spec to validate arg parsing without launching a session.
	code := runWithDeps([]string{"run", "ralph:25", "my-session", "--explain-spec", "--json"}, deps)
	if code == output.ExitInvalidArgs {
		t.Fatalf("got invalid args error with iteration count; stderr: %s", stderr.String())
	}
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
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

func TestRun_OnEscalateFlag(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--on-escalate", "webhook:http://localhost:8123/hook", "--explain-spec", "--json"}, deps)
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
	if request["on_escalate"] != "webhook:http://localhost:8123/hook" {
		t.Errorf("request.on_escalate = %v, want webhook override", request["on_escalate"])
	}
}

func TestRun_OnEscalateFlag_Invalid(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--on-escalate", "wat", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
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

func TestRun_ProviderFlag_Codex(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "codex", "--explain-spec", "--json"}, deps)
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
	if request["provider"] != "codex" {
		t.Errorf("request.provider = %v, want codex", request["provider"])
	}
}

func TestRun_ProviderFlag_OpenAIAlias(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "openai", "--explain-spec", "--json"}, deps)
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
	// "openai" should be normalized to "codex".
	if request["provider"] != "codex" {
		t.Errorf("request.provider = %v, want codex", request["provider"])
	}
}

func TestRun_ProviderFlag_Invalid(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "gpt-x", "--fg"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitInvalidArgs, stderr.String())
	}
	if !containsSubstring(stderr.String(), "UNKNOWN_PROVIDER") {
		t.Errorf("expected UNKNOWN_PROVIDER in stderr: %s", stderr.String())
	}
}

func TestRun_ProviderFlag_Invalid_JSON(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "gpt-x", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "UNKNOWN_PROVIDER" {
		t.Errorf("error code = %v, want UNKNOWN_PROVIDER", errObj["code"])
	}
	// Output layer flattens Available map: {"providers": [...]} → "available_providers" key.
	providers, ok := errObj["available_providers"]
	if !ok {
		t.Fatal("missing available_providers field in error")
	}
	provList, ok := providers.([]any)
	if !ok || len(provList) < 2 {
		t.Errorf("expected at least 2 providers in available_providers, got: %v", providers)
	}
}

func TestResolveProviderName_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		cli    string
		stage  string
		config string
		want   string
	}{
		{"cli wins over all", "codex", "claude", "claude", "codex"},
		{"stage wins over config", "", "codex", "claude", "codex"},
		{"config wins over default", "", "", "codex", "codex"},
		{"default is claude", "", "", "", "claude"},
		{"cli empty falls through", "", "codex", "", "codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveProviderName(tt.cli, tt.stage, tt.config)
			if got != tt.want {
				t.Errorf("resolveProviderName(%q, %q, %q) = %q, want %q",
					tt.cli, tt.stage, tt.config, got, tt.want)
			}
		})
	}
}

func TestValidateProviderName(t *testing.T) {
	// Valid providers return nil.
	for _, name := range []string{"claude", "codex", "anthropic", "openai"} {
		if err := validateProviderName(name); err != nil {
			t.Errorf("validateProviderName(%q) returned error, want nil", name)
		}
	}
	// Invalid provider returns error.
	if err := validateProviderName("gpt-x"); err == nil {
		t.Error("validateProviderName(gpt-x) returned nil, want error")
	}
}

func TestResolveModelName_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		cli     string
		stage   string
		provDef string
		want    string
	}{
		{"cli wins over all", "opus", "sonnet", "haiku", "opus"},
		{"stage wins over provider default", "", "sonnet", "haiku", "sonnet"},
		{"provider default wins over empty", "", "", "haiku", "haiku"},
		{"all empty returns empty", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveModelName(tt.cli, tt.stage, tt.provDef)
			if got != tt.want {
				t.Errorf("resolveModelName(%q, %q, %q) = %q, want %q", tt.cli, tt.stage, tt.provDef, got, tt.want)
			}
		})
	}
}

func TestValidateModelForProvider_Claude(t *testing.T) {
	// Valid models.
	for _, model := range []string{"opus", "sonnet", "haiku", "claude-opus", "opus-4"} {
		if err := validateModelForProvider(model, "claude"); err != nil {
			t.Errorf("validateModelForProvider(%q, claude) unexpected error: %v", model, err)
		}
	}

	// Invalid model.
	errResp := validateModelForProvider("gpt-5.3-codex", "claude")
	if errResp == nil {
		t.Fatal("expected error for codex model on claude provider")
	}
	if errResp.Error.Code != "UNKNOWN_MODEL" {
		t.Errorf("error code = %q, want UNKNOWN_MODEL", errResp.Error.Code)
	}
}

func TestValidateModelForProvider_Codex(t *testing.T) {
	// Valid models.
	for _, model := range []string{"gpt-5.3-codex", "o3", "o3-mini"} {
		if err := validateModelForProvider(model, "codex"); err != nil {
			t.Errorf("validateModelForProvider(%q, codex) unexpected error: %v", model, err)
		}
	}

	// Valid model with reasoning suffix.
	if err := validateModelForProvider("gpt-5.2-codex:xhigh", "codex"); err != nil {
		t.Errorf("validateModelForProvider(gpt-5.2-codex:xhigh, codex) unexpected error: %v", err)
	}

	// Invalid model.
	errResp := validateModelForProvider("opus", "codex")
	if errResp == nil {
		t.Fatal("expected error for claude model on codex provider")
	}
	if errResp.Error.Code != "UNKNOWN_MODEL" {
		t.Errorf("error code = %q, want UNKNOWN_MODEL", errResp.Error.Code)
	}
}

func TestValidateModelForProvider_EmptySkips(t *testing.T) {
	// Empty model should always pass (provider uses its default).
	if err := validateModelForProvider("", "claude"); err != nil {
		t.Errorf("empty model should pass: %v", err)
	}
}

func TestRun_ModelFlag_Invalid(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "-m", "nonexistent-model", "--fg"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, output.ExitInvalidArgs, stderr.String())
	}
	if !containsSubstring(stderr.String(), "UNKNOWN_MODEL") {
		t.Errorf("expected UNKNOWN_MODEL in stderr: %s", stderr.String())
	}
}

func TestRun_ModelFlag_Invalid_JSON(t *testing.T) {
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

	code := runWithDeps([]string{"run", "ralph", "my-session", "-m", "bad-model", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "UNKNOWN_MODEL" {
		t.Errorf("error code = %v, want UNKNOWN_MODEL", errObj["code"])
	}
	// Output layer flattens Available map: {"models": [...]} → "available_models" key.
	if _, ok := errObj["available_models"]; !ok {
		t.Fatal("missing available_models field in error")
	}
}

// --- Stub launcher for testing run→launch path ---

type testLauncher struct {
	handle    session.SessionHandle
	err       error
	available bool

	calls   int
	session string
	cmd     []string
	opts    session.StartOptions
}

func (l *testLauncher) Start(sess string, runnerCmd []string, opts session.StartOptions) (session.SessionHandle, error) {
	l.calls++
	l.session = sess
	l.cmd = append([]string(nil), runnerCmd...)
	l.opts = opts
	if l.err != nil {
		return session.SessionHandle{}, l.err
	}
	return l.handle, nil
}

func (l *testLauncher) Kill(string) error { return nil }
func (l *testLauncher) Available() bool   { return l.available }
func (l *testLauncher) Name() string      { return "test" }

func TestRun_BackgroundLaunch_JSON(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		handle: session.SessionHandle{
			Session: "my-session",
			PID:     9876,
			Backend: "test",
		},
	}
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}

	if result["launched"] != true {
		t.Errorf("expected launched=true, got %v", result["launched"])
	}
	if result["session"] != "my-session" {
		t.Errorf("session = %v, want my-session", result["session"])
	}
	if result["launcher"] != "test" {
		t.Errorf("launcher = %v, want test", result["launcher"])
	}
	if launcher.calls != 1 {
		t.Errorf("launcher calls = %d, want 1", launcher.calls)
	}
}

func TestRun_BackgroundLaunch_ProjectRootFlag(t *testing.T) {
	dir := setupStageDir(t)
	overrideRoot := t.TempDir()
	stageDir := filepath.Join(overrideRoot, ".claude", "stages", "ralph")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte("name: ralph\ndescription: override\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte("override prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		handle: session.SessionHandle{
			Session: "override-session",
			PID:     9999,
			Backend: "test",
		},
	}
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
	}

	code := runWithDeps([]string{"run", "ralph", "override-session", "--project-root", overrideRoot, "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if launcher.opts.WorkDir != overrideRoot {
		t.Fatalf("launcher opts.WorkDir = %q, want %q", launcher.opts.WorkDir, overrideRoot)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["project_root"] != overrideRoot {
		t.Fatalf("project_root = %v, want %q", result["project_root"], overrideRoot)
	}
	request, ok := result["request"].(map[string]any)
	if !ok {
		t.Fatal("missing request object")
	}
	if request["project_root"] != overrideRoot {
		t.Fatalf("request.project_root = %v, want %q", request["project_root"], overrideRoot)
	}
}

func TestRun_BackgroundLaunch_Human(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		handle: session.SessionHandle{
			Session: "my-session",
			PID:     9876,
			Backend: "test",
		},
	}
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
	}

	code := runWithDeps([]string{"run", "ralph", "my-session"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !containsSubstring(out, "started") {
		t.Errorf("expected 'started' in output: %s", out)
	}
	if !containsSubstring(out, "my-session") {
		t.Errorf("expected session name in output: %s", out)
	}
}

func TestRun_LaunchError_JSON(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		err:       fmt.Errorf("tmux exploded"),
	}
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--json"}, deps)
	if code != output.ExitGeneralError {
		t.Fatalf("exit code = %d, want %d", code, output.ExitGeneralError)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "START_FAILED" {
		t.Errorf("error code = %v, want START_FAILED", errObj["code"])
	}
}

func TestRun_LauncherUnavailable_FallsToForeground(t *testing.T) {
	// When launcher.Available() is false in background mode, runRun falls back
	// to runForeground which also checks Available() and returns an error.
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: false,
	}
	deps := cliDeps{
		mode:   output.ModeHuman,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
	}

	code := runWithDeps([]string{"run", "ralph", "my-session"}, deps)
	if code != output.ExitGeneralError {
		t.Fatalf("exit code = %d, want %d; stdout: %s; stderr: %s", code, output.ExitGeneralError, stdout.String(), stderr.String())
	}
	if !containsSubstring(stderr.String(), "not available") {
		t.Errorf("expected 'not available' in stderr: %s", stderr.String())
	}
}

func TestRun_Foreground_PollsStore(t *testing.T) {
	dir := setupStageDir(t)
	s, err := store.Open(filepath.Join(dir, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		handle: session.SessionHandle{
			Session: "fg-session",
			PID:     5555,
			Backend: "test",
		},
	}
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
		store:    s,
	}

	// Run foreground in a goroutine since it polls.
	done := make(chan int, 1)
	go func() {
		done <- runWithDeps([]string{"run", "ralph", "fg-session", "--fg", "--json"}, deps)
	}()

	// The launcher is called synchronously, so wait for it.
	for i := 0; i < 50; i++ {
		if launcher.calls > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if launcher.calls == 0 {
		t.Fatal("launcher was never called")
	}

	// Create the session in the store and mark it completed (simulating what _run would do).
	ctx := context.Background()
	_ = s.CreateSession(ctx, "fg-session", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "fg-session", map[string]any{
		"status":              "completed",
		"iteration_completed": 3,
	})

	select {
	case code := <-done:
		if code != output.ExitSuccess {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreground polling did not complete within timeout")
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["status"] != "completed" {
		t.Errorf("status = %v, want completed", result["status"])
	}
	if result["session"] != "fg-session" {
		t.Errorf("session = %v, want fg-session", result["session"])
	}
}

func TestRun_Foreground_FailedStatus(t *testing.T) {
	dir := setupStageDir(t)
	s, err := store.Open(filepath.Join(dir, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var stdout, stderr bytes.Buffer
	launcher := &testLauncher{
		available: true,
		handle: session.SessionHandle{
			Session: "fail-session",
			PID:     6666,
			Backend: "test",
		},
	}
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
		launcher: launcher,
		store:    s,
	}

	done := make(chan int, 1)
	go func() {
		done <- runWithDeps([]string{"run", "ralph", "fail-session", "--fg", "--json"}, deps)
	}()

	for i := 0; i < 50; i++ {
		if launcher.calls > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Create the session in the store and mark it failed (simulating what _run would do).
	ctx := context.Background()
	_ = s.CreateSession(ctx, "fail-session", "loop", "", "{}")
	_ = s.UpdateSession(ctx, "fail-session", map[string]any{
		"status":              "failed",
		"iteration_completed": 1,
	})

	select {
	case code := <-done:
		if code != output.ExitProviderError {
			t.Fatalf("exit code = %d, want %d", code, output.ExitProviderError)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreground polling did not complete within timeout")
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v; output: %s", err, stdout.String())
	}
	if result["status"] != "failed" {
		t.Errorf("status = %v, want failed", result["status"])
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
