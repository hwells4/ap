package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunHook_EmptyCommand(t *testing.T) {
	err := RunHook(context.Background(), "test", "", t.TempDir(), nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook empty command: expected nil, got %v", err)
	}
}

func TestRunHook_WhitespaceCommand(t *testing.T) {
	err := RunHook(context.Background(), "test", "   ", t.TempDir(), nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook whitespace command: expected nil, got %v", err)
	}
}

func TestRunHook_VariableSubstitution(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "marker.txt")

	vars := map[string]string{
		"SESSION":   "test-session",
		"STAGE":     "ralph",
		"ITERATION": "3",
		"STATUS":    "running",
	}

	// ${VAR} substitutions are shell-quoted for safety. Use them as standalone
	// arguments (not embedded inside other quotes).
	cmd := "echo ${SESSION} ${STAGE} ${ITERATION} ${STATUS} > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	content := strings.TrimSpace(string(data))
	expected := "test-session ralph 3 running"
	if content != expected {
		t.Fatalf("marker content = %q, want %q", content, expected)
	}
}

func TestRunHook_CreatesMarkerFile(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "hook-ran.txt")

	err := RunHook(context.Background(), "post_iteration", "touch "+markerPath, dir, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Fatal("expected marker file to exist")
	}
}

func TestRunHook_FailureReturnsError(t *testing.T) {
	err := RunHook(context.Background(), "bad_hook", "exit 1", t.TempDir(), nil, 10*time.Second)
	if err == nil {
		t.Fatal("expected error from failed hook")
	}
	if !strings.Contains(err.Error(), "hook bad_hook failed") {
		t.Fatalf("error = %q, want it to contain 'hook bad_hook failed'", err.Error())
	}
}

func TestRunHook_DefaultTimeoutApplied(t *testing.T) {
	// Verify that a zero timeout defaults to 60s (by checking the hook runs successfully
	// with a fast command and zero timeout).
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "default-timeout.txt")
	err := RunHook(context.Background(), "test", "touch "+markerPath, dir, nil, 0)
	if err != nil {
		t.Fatalf("RunHook with zero timeout: %v", err)
	}
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Fatal("expected marker file from zero-timeout hook")
	}
}

func TestRunHook_WorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "pwd.txt")

	err := RunHook(context.Background(), "test", "pwd > "+markerPath, dir, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read pwd: %v", err)
	}

	got := strings.TrimSpace(string(data))
	if got != dir {
		t.Fatalf("working dir = %q, want %q", got, dir)
	}
}

func TestRunHook_EnvironmentVariables(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "env.txt")

	vars := map[string]string{
		"SESSION": "my-session",
		"STAGE":   "my-stage",
	}

	cmd := "echo $AP_SESSION:$AP_STAGE > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}

	content := strings.TrimSpace(string(data))
	expected := "my-session:my-stage"
	if content != expected {
		t.Fatalf("env content = %q, want %q", content, expected)
	}
}

func TestRunHook_InheritsParentEnvironment(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "parent-env.txt")

	// Set a custom env var in the parent process.
	t.Setenv("AP_TEST_PARENT_VAR", "inherited-value")

	vars := map[string]string{"SESSION": "s1"}
	cmd := "echo $AP_TEST_PARENT_VAR > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	content := strings.TrimSpace(string(data))
	if content != "inherited-value" {
		t.Fatalf("parent env not inherited: got %q, want %q", content, "inherited-value")
	}
}

func TestRunHook_SummaryVariable(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "summary.txt")

	vars := map[string]string{
		"SESSION": "s1",
		"SUMMARY": "Implemented feature X and fixed bug Y",
	}

	// ${SUMMARY} is shell-quoted, so use it as a standalone arg (no extra quoting).
	cmd := "echo ${SUMMARY} > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	content := strings.TrimSpace(string(data))
	expected := "Implemented feature X and fixed bug Y"
	if content != expected {
		t.Fatalf("summary = %q, want %q", content, expected)
	}
}

func TestRunHook_SummaryEnvVar(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "summary-env.txt")

	vars := map[string]string{
		"SESSION": "s1",
		"SUMMARY": "Added login page",
	}

	// Use $AP_SUMMARY env var (safer than ${SUMMARY} template for shell-unsafe content).
	cmd := "echo $AP_SUMMARY > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	content := strings.TrimSpace(string(data))
	if content != "Added login page" {
		t.Fatalf("AP_SUMMARY env = %q, want %q", content, "Added login page")
	}
}

func TestLifecycleHooks_IsEmpty(t *testing.T) {
	empty := LifecycleHooks{}
	if !empty.IsEmpty() {
		t.Fatal("expected empty hooks to be empty")
	}

	notEmpty := LifecycleHooks{PostIteration: "git commit"}
	if notEmpty.IsEmpty() {
		t.Fatal("expected non-empty hooks to not be empty")
	}
}

func TestLifecycleHooks_Command(t *testing.T) {
	hooks := LifecycleHooks{
		PreSession:    "cmd-pre-session",
		PreIteration:  "cmd-pre-iteration",
		PreStage:      "cmd-pre-stage",
		PostIteration: "cmd-post-iteration",
		PostStage:     "cmd-post-stage",
		PostSession:   "cmd-post-session",
		OnFailure:     "cmd-on-failure",
	}

	tests := []struct {
		name string
		want string
	}{
		{"pre_session", "cmd-pre-session"},
		{"pre_iteration", "cmd-pre-iteration"},
		{"pre_stage", "cmd-pre-stage"},
		{"post_iteration", "cmd-post-iteration"},
		{"post_stage", "cmd-post-stage"},
		{"post_session", "cmd-post-session"},
		{"on_failure", "cmd-on-failure"},
		{"nonexistent", ""},
	}

	for _, tt := range tests {
		got := hooks.Command(tt.name)
		if got != tt.want {
			t.Errorf("Command(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestLifecycleHooks_ApplyOverrides(t *testing.T) {
	hooks := LifecycleHooks{
		PreSession:    "global-pre",
		PostIteration: "global-post-iter",
	}

	// Override some, leave others.
	hooks.ApplyOverrides(map[string]string{
		"pre_session":    "pipeline-pre",
		"pre_iteration":  "pipeline-pre-iter",
		"post_iteration": "",       // empty — should NOT override
		"on_failure":     "cleanup",
	})

	if hooks.PreSession != "pipeline-pre" {
		t.Errorf("PreSession = %q, want pipeline-pre", hooks.PreSession)
	}
	if hooks.PreIteration != "pipeline-pre-iter" {
		t.Errorf("PreIteration = %q, want pipeline-pre-iter", hooks.PreIteration)
	}
	if hooks.PostIteration != "global-post-iter" {
		t.Errorf("PostIteration should be unchanged, got %q", hooks.PostIteration)
	}
	if hooks.OnFailure != "cleanup" {
		t.Errorf("OnFailure = %q, want cleanup", hooks.OnFailure)
	}
}

func TestHookContext_VarsAlwaysPresent(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "all-vars.txt")

	cfg := Config{
		Session:     "test-session",
		WorkDir:     dir,
		HookTimeout: 10 * time.Second,
		Hooks: LifecycleHooks{
			PreIteration: "echo ${SESSION}:${STAGE}:${ITERATION}:${STATUS}:${SUMMARY} > " + markerPath,
		},
	}

	hc := NewHookContext(cfg)
	hc.SetStage("my-stage")
	hc.SetIteration(3)
	hc.SetSummary("did stuff")
	hc.Fire(context.Background(), "pre_iteration")

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	content := strings.TrimSpace(string(data))
	expected := "test-session:my-stage:3:running:did stuff"
	if content != expected {
		t.Fatalf("vars = %q, want %q", content, expected)
	}
}

func TestHookContext_FireSkipsEmptyHook(t *testing.T) {
	cfg := Config{
		Session: "test",
		WorkDir: t.TempDir(),
		Hooks:   LifecycleHooks{}, // all empty
	}

	hc := NewHookContext(cfg)
	// Should not panic or error — just a no-op.
	hc.Fire(context.Background(), "pre_iteration")
	hc.Fire(context.Background(), "post_session")
	hc.Fire(context.Background(), "on_failure")
}

func TestHookContext_StateAccumulates(t *testing.T) {
	dir := t.TempDir()

	cfg := Config{
		Session:     "accum-test",
		WorkDir:     dir,
		HookTimeout: 10 * time.Second,
		Hooks: LifecycleHooks{
			PostIteration: "echo ${ITERATION}:${SUMMARY} >> " + filepath.Join(dir, "log.txt"),
			PostSession:   "echo final:${SUMMARY} >> " + filepath.Join(dir, "log.txt"),
		},
	}

	hc := NewHookContext(cfg)
	hc.SetStage("codegen")

	// Simulate 3 iterations.
	for i := 1; i <= 3; i++ {
		hc.SetIteration(i)
		hc.SetSummary("iter-" + strings.Repeat("x", i))
		hc.Fire(context.Background(), "post_iteration")
	}
	hc.SetStatus("completed")
	hc.Fire(context.Background(), "post_session")

	data, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "1:iter-x" {
		t.Errorf("line 0 = %q, want 1:iter-x", lines[0])
	}
	if lines[1] != "2:iter-xx" {
		t.Errorf("line 1 = %q, want 2:iter-xx", lines[1])
	}
	if lines[2] != "3:iter-xxx" {
		t.Errorf("line 2 = %q, want 3:iter-xxx", lines[2])
	}
	if lines[3] != "final:iter-xxx" {
		t.Errorf("line 3 = %q, want final:iter-xxx (last summary persists)", lines[3])
	}
}
