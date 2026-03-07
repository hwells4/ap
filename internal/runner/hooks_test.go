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

func TestRunHook_ShellInjectionSafety(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "injection.txt")
	maliciousMarker := filepath.Join(dir, "pwned.txt")

	vars := map[string]string{
		"SESSION": "safe",
		"SUMMARY": "$(touch " + maliciousMarker + ")",
	}

	// ${SUMMARY} is shell-quoted; the subshell should NOT execute.
	cmd := "echo ${SUMMARY} > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	// The malicious marker should NOT exist.
	if _, err := os.Stat(maliciousMarker); err == nil {
		t.Fatal("shell injection succeeded — malicious marker file was created")
	}

	// The literal text should have been written.
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.Contains(content, "touch") {
		t.Fatalf("expected literal shell text, got %q", content)
	}
}

func TestRunHook_SummaryWithSpecialChars(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "special.txt")

	vars := map[string]string{
		"SESSION": "s1",
		"SUMMARY": "feat: add user's auth & fix bug #42",
	}

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
	if content != "feat: add user's auth & fix bug #42" {
		t.Fatalf("special chars mangled: got %q", content)
	}
}

func TestRunHook_FailingCommandStderr(t *testing.T) {
	err := RunHook(context.Background(), "stderr_hook", "echo 'oops' >&2; exit 1", t.TempDir(), nil, 10*time.Second)
	if err == nil {
		t.Fatal("expected error from failing hook with stderr")
	}
	// Error message should include stderr output.
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("error = %q, want it to include stderr 'oops'", err.Error())
	}
}

func TestRunHook_FailingCommandStdoutFallback(t *testing.T) {
	// When stderr is empty, stdout should appear in the error detail.
	err := RunHook(context.Background(), "stdout_hook", "echo 'helpful info'; exit 1", t.TempDir(), nil, 10*time.Second)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
	if !strings.Contains(err.Error(), "helpful info") {
		t.Fatalf("error = %q, want it to include stdout 'helpful info'", err.Error())
	}
}

func TestRunHook_NegativeTimeout(t *testing.T) {
	// Negative timeout should be treated like zero (default to 60s).
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "neg-timeout.txt")
	err := RunHook(context.Background(), "test", "touch "+markerPath, dir, nil, -1*time.Second)
	if err != nil {
		t.Fatalf("RunHook with negative timeout: %v", err)
	}
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Fatal("expected marker file from negative-timeout hook")
	}
}

func TestLifecycleHooks_ApplyOverrides_WhitespaceOnly(t *testing.T) {
	hooks := LifecycleHooks{
		PreSession: "original",
	}
	hooks.ApplyOverrides(map[string]string{
		"pre_session": "   ", // whitespace-only should NOT override
	})
	if hooks.PreSession != "original" {
		t.Errorf("PreSession = %q, want original (whitespace override should be ignored)", hooks.PreSession)
	}
}

func TestLifecycleHooks_ApplyOverrides_UnknownKey(t *testing.T) {
	hooks := LifecycleHooks{
		PreSession: "original",
	}
	hooks.ApplyOverrides(map[string]string{
		"unknown_hook": "should be ignored",
	})
	if hooks.PreSession != "original" {
		t.Errorf("PreSession = %q, want original", hooks.PreSession)
	}
}

func TestLifecycleHooks_Command_AllHookNames(t *testing.T) {
	// Exhaustive: every valid hook name returns its value.
	hooks := LifecycleHooks{
		PreSession:    "a",
		PreIteration:  "b",
		PreStage:      "c",
		PostIteration: "d",
		PostStage:     "e",
		PostSession:   "f",
		OnFailure:     "g",
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{"pre_session", "a"},
		{"pre_iteration", "b"},
		{"pre_stage", "c"},
		{"post_iteration", "d"},
		{"post_stage", "e"},
		{"post_session", "f"},
		{"on_failure", "g"},
		{"", ""},
		{"random", ""},
	} {
		got := hooks.Command(tc.name)
		if got != tc.want {
			t.Errorf("Command(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunHook_NilVars(t *testing.T) {
	// Passing nil vars should work (no variable substitution).
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "nil-vars.txt")
	err := RunHook(context.Background(), "test", "echo hello > "+markerPath, dir, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook with nil vars: %v", err)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if strings.TrimSpace(string(data)) != "hello" {
		t.Fatalf("got %q, want hello", strings.TrimSpace(string(data)))
	}
}

func TestRunHook_EmptyVars(t *testing.T) {
	// Passing an empty vars map should work.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "empty-vars.txt")
	err := RunHook(context.Background(), "test", "echo ok > "+markerPath, dir, map[string]string{}, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook with empty vars: %v", err)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("got %q, want ok", strings.TrimSpace(string(data)))
	}
}

func TestLifecycleHooks_ApplyOverrides_AllFields(t *testing.T) {
	// Override all 7 hook fields at once.
	hooks := LifecycleHooks{}
	hooks.ApplyOverrides(map[string]string{
		"pre_session":    "a",
		"pre_iteration":  "b",
		"pre_stage":      "c",
		"post_iteration": "d",
		"post_stage":     "e",
		"post_session":   "f",
		"on_failure":     "g",
	})
	if hooks.PreSession != "a" {
		t.Errorf("PreSession = %q, want a", hooks.PreSession)
	}
	if hooks.PreIteration != "b" {
		t.Errorf("PreIteration = %q, want b", hooks.PreIteration)
	}
	if hooks.PreStage != "c" {
		t.Errorf("PreStage = %q, want c", hooks.PreStage)
	}
	if hooks.PostIteration != "d" {
		t.Errorf("PostIteration = %q, want d", hooks.PostIteration)
	}
	if hooks.PostStage != "e" {
		t.Errorf("PostStage = %q, want e", hooks.PostStage)
	}
	if hooks.PostSession != "f" {
		t.Errorf("PostSession = %q, want f", hooks.PostSession)
	}
	if hooks.OnFailure != "g" {
		t.Errorf("OnFailure = %q, want g", hooks.OnFailure)
	}
	if hooks.IsEmpty() {
		t.Error("hooks should not be empty after override")
	}
}

func TestLifecycleHooks_IsEmpty_EachField(t *testing.T) {
	// Verify IsEmpty returns false when ANY single field is set.
	fields := []struct {
		name string
		fn   func() LifecycleHooks
	}{
		{"PreSession", func() LifecycleHooks { return LifecycleHooks{PreSession: "x"} }},
		{"PreIteration", func() LifecycleHooks { return LifecycleHooks{PreIteration: "x"} }},
		{"PreStage", func() LifecycleHooks { return LifecycleHooks{PreStage: "x"} }},
		{"PostIteration", func() LifecycleHooks { return LifecycleHooks{PostIteration: "x"} }},
		{"PostStage", func() LifecycleHooks { return LifecycleHooks{PostStage: "x"} }},
		{"PostSession", func() LifecycleHooks { return LifecycleHooks{PostSession: "x"} }},
		{"OnFailure", func() LifecycleHooks { return LifecycleHooks{OnFailure: "x"} }},
	}
	for _, f := range fields {
		h := f.fn()
		if h.IsEmpty() {
			t.Errorf("IsEmpty() = true when %s is set", f.name)
		}
	}
}

func TestRunHook_BacktickInjectionSafety(t *testing.T) {
	// Backtick command substitution should also be prevented.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "backtick.txt")
	maliciousMarker := filepath.Join(dir, "pwned-backtick.txt")

	vars := map[string]string{
		"SESSION": "safe",
		"SUMMARY": "`touch " + maliciousMarker + "`",
	}

	cmd := "echo ${SUMMARY} > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	// The malicious marker should NOT exist.
	if _, err := os.Stat(maliciousMarker); err == nil {
		t.Fatal("backtick injection succeeded — malicious marker file was created")
	}
}

func TestRunHook_MultilineCommand(t *testing.T) {
	// Verify multi-line commands work (sh -c handles them).
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "multiline.txt")
	cmd := "echo first > " + markerPath + "\necho second >> " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("multiline result = %v, want [first, second]", lines)
	}
}

func TestRunHook_PipeInjectionSafety(t *testing.T) {
	// Pipe operator in SUMMARY should not execute as a pipe.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "pipe.txt")

	vars := map[string]string{
		"SESSION": "safe",
		"SUMMARY": "hello | cat /etc/passwd",
	}

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
	// The literal text should appear, including the pipe character.
	if !strings.Contains(content, "|") {
		t.Fatalf("pipe char lost: got %q", content)
	}
	if !strings.Contains(content, "cat /etc/passwd") {
		t.Fatalf("expected literal pipe text, got %q", content)
	}
}

func TestRunHook_SemicolonInjectionSafety(t *testing.T) {
	// Semicolons in SUMMARY should not execute as separate commands.
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "semicolon.txt")
	maliciousMarker := filepath.Join(dir, "pwned-semi.txt")

	vars := map[string]string{
		"SESSION": "safe",
		"SUMMARY": "done; touch " + maliciousMarker,
	}

	cmd := "echo ${SUMMARY} > " + markerPath
	err := RunHook(context.Background(), "test", cmd, dir, vars, 10*time.Second)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}

	// The malicious marker should NOT exist.
	if _, err := os.Stat(maliciousMarker); err == nil {
		t.Fatal("semicolon injection succeeded — malicious marker file was created")
	}
}

func TestNewHookContext_DefaultStatus(t *testing.T) {
	cfg := Config{Session: "test"}
	hc := NewHookContext(cfg)
	vars := hc.vars()
	if vars["STATUS"] != "running" {
		t.Errorf("default status = %q, want running", vars["STATUS"])
	}
}

func TestHookContext_SettersUpdateVars(t *testing.T) {
	cfg := Config{Session: "test-session"}
	hc := NewHookContext(cfg)

	hc.SetStage("my-stage")
	hc.SetIteration(5)
	hc.SetStatus("completed")
	hc.SetSummary("all done")

	vars := hc.vars()
	if vars["SESSION"] != "test-session" {
		t.Errorf("SESSION = %q, want test-session", vars["SESSION"])
	}
	if vars["STAGE"] != "my-stage" {
		t.Errorf("STAGE = %q, want my-stage", vars["STAGE"])
	}
	if vars["ITERATION"] != "5" {
		t.Errorf("ITERATION = %q, want 5", vars["ITERATION"])
	}
	if vars["STATUS"] != "completed" {
		t.Errorf("STATUS = %q, want completed", vars["STATUS"])
	}
	if vars["SUMMARY"] != "all done" {
		t.Errorf("SUMMARY = %q, want all done", vars["SUMMARY"])
	}
}
