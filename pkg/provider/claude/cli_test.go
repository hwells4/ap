package claude

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

func TestExecuteCancellationStopsLongRunningCommand(t *testing.T) {
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

func TestExecuteBoundsOutputStreams(t *testing.T) {
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
	if len(result.Stderr) > int(internalexec.DefaultMaxOutput) {
		t.Fatalf("stderr exceeded bound: %d > %d", len(result.Stderr), internalexec.DefaultMaxOutput)
	}
}

func newTestCLI(t *testing.T, body string) *CLI {
	t.Helper()

	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + strings.TrimSpace(body) + "\n"
	binary := writeFakeClaude(t, script)
	cli := New(WithBinary(binary), WithSkipPermissions(false))

	if err := cli.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := cli.Init(context.Background()); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Shutdown(context.Background())
	})
	return cli
}

func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude binary: %v", err)
	}
	return path
}
