package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalexec "github.com/hwells4/ap/internal/exec"
	"github.com/hwells4/ap/pkg/provider"
)

func TestName(t *testing.T) {
	cli := New()
	if cli.Name() != "codex" {
		t.Errorf("Name() = %q, want codex", cli.Name())
	}
}

func TestDefaultModel(t *testing.T) {
	cli := New()
	if cli.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel() = %q, want %q", cli.DefaultModel(), DefaultModel)
	}
}

func TestDefaultModelOverride(t *testing.T) {
	cli := New(WithDefaultModel("o3"))
	if cli.DefaultModel() != "o3" {
		t.Errorf("DefaultModel() = %q, want o3", cli.DefaultModel())
	}
}

func TestExecuteCapturesStdoutAndStderr(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
echo "stdout-line"
echo "stderr-line" >&2
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if result.Stdout != "stdout-line\n" {
		t.Fatalf("stdout mismatch: %q", result.Stdout)
	}
	if result.Stderr != "stderr-line\n" {
		t.Fatalf("stderr mismatch: %q", result.Stderr)
	}
	if result.Output != result.Stdout {
		t.Fatalf("legacy Output should mirror Stdout")
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestExecuteCommandConstruction(t *testing.T) {
	// Verify the command includes bypass and ephemeral flags.
	cli := newTestCLI(t, `
cat >/dev/null
echo "$@"
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "test prompt",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	args := result.Stdout
	if !strings.Contains(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("missing --dangerously-bypass-approvals-and-sandbox flag in: %s", args)
	}
	if !strings.Contains(args, "--ephemeral") {
		t.Errorf("missing --ephemeral flag in: %s", args)
	}
	// The "-" flag tells codex to read from stdin.
	if !strings.Contains(args, " -") {
		t.Errorf("missing stdin flag (-) in: %s", args)
	}
}

func TestExecuteModelOverride(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
echo "$@"
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "test",
		Model:  "o3",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(result.Stdout, "--model o3") {
		t.Errorf("model override not in args: %s", result.Stdout)
	}
}

func TestExecuteUsesDefaultModelWhenEmpty(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
echo "$@"
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(result.Stdout, "--model "+DefaultModel) {
		t.Errorf("default model not in args: %s", result.Stdout)
	}
}

func TestExecuteReceivesPromptOnStdin(t *testing.T) {
	cli := newTestCLI(t, `
# Read stdin and echo it back.
cat
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "the test prompt",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(result.Stdout, "the test prompt") {
		t.Errorf("stdin prompt not received: %q", result.Stdout)
	}
}

func TestExecuteNonZeroExitCode(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
exit 1
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "test",
	})
	// Should still get a result even with non-zero exit.
	if err == nil {
		t.Fatal("expected error for non-zero exit code")
	}
	if result.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", result.ExitCode)
	}
}

func TestExecuteWorkDir(t *testing.T) {
	tmpDir := t.TempDir()
	cli := newTestCLI(t, `
cat >/dev/null
pwd
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt:  "test",
		WorkDir: tmpDir,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	got := strings.TrimSpace(result.Stdout)
	if got != tmpDir {
		t.Errorf("workdir = %q, want %q", got, tmpDir)
	}
}

func TestExecuteEnvVars(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
echo "AP_SESSION=$AP_SESSION"
`)

	result, err := cli.Execute(context.Background(), provider.Request{
		Prompt: "test",
		Env:    map[string]string{"AP_SESSION": "my-session"},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(result.Stdout, "AP_SESSION=my-session") {
		t.Errorf("env var not set: %q", result.Stdout)
	}
}

func TestExecuteCancellation(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
while true; do :; done
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	_, err := cli.Execute(ctx, provider.Request{Prompt: "hello"})
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if time.Since(started) > 10*time.Second {
		t.Fatalf("execute took too long to cancel")
	}
}

func TestExecuteBoundsOutput(t *testing.T) {
	cli := newTestCLI(t, `
cat >/dev/null
head -c 1048576 </dev/zero | tr '\0' 'a'
head -c 1048576 </dev/zero | tr '\0' 'b' >&2
`)

	result, err := cli.Execute(context.Background(), provider.Request{Prompt: "hello"})
	if !errors.Is(err, internalexec.ErrOutputTruncated) {
		t.Fatalf("expected output truncation error, got: %v", err)
	}
	if len(result.Stdout) > int(internalexec.DefaultMaxOutput) {
		t.Fatalf("stdout exceeded bound: %d > %d", len(result.Stdout), internalexec.DefaultMaxOutput)
	}
}

func TestModelNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gpt-5-codex", "gpt-5-codex"},
		{"GPT-5-CODEX", "gpt-5-codex"},
		{"o3", "o3"},
		{"o3-mini", "o3-mini"},
		{"gpt-5.3-codex", "gpt-5.3-codex"},
	}
	for _, tt := range tests {
		got := normalizeModel(tt.input)
		if got != tt.want {
			t.Errorf("normalizeModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCapabilities(t *testing.T) {
	cli := New()
	caps := cli.Capabilities()
	if !caps.Has(provider.CapabilityTools) {
		t.Error("expected CapabilityTools")
	}
	if len(caps.SupportedModels) == 0 {
		t.Error("expected non-empty supported models")
	}
}

func TestValidate(t *testing.T) {
	// Valid config.
	cli := New(WithBinary("codex"))
	if err := cli.Validate(); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}

	// Invalid binary.
	cli2 := New(WithBinary("bad-binary"))
	if err := cli2.Validate(); err == nil {
		t.Error("expected error for invalid binary")
	}
}

func TestWithBypass(t *testing.T) {
	cli := New(WithBypass(false))
	if cli.Bypass {
		t.Error("expected Bypass=false")
	}
}

// --- test helpers ---

func newTestCLI(t *testing.T, body string) *CLI {
	t.Helper()

	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + strings.TrimSpace(body) + "\n"
	binary := writeFakeCodex(t, script)
	cli := New(WithBinary(binary), WithBypass(true))

	if err := cli.Init(context.Background()); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Shutdown(context.Background())
	})
	return cli
}

func writeFakeCodex(t *testing.T, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex binary: %v", err)
	}
	return path
}
