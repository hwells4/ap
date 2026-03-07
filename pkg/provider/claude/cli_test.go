package claude

import (
	"context"
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
	if err != nil {
		t.Fatalf("expected no error (truncation is informational), got: %v", err)
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

func TestResolveModel_Aliases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"opus", "opus"},
		{"claude-opus", "opus"},
		{"opus-4", "opus"},
		{"opus-4.5", "opus"},
		{"sonnet", "sonnet"},
		{"claude-sonnet", "sonnet"},
		{"sonnet-4", "sonnet"},
		{"haiku", "haiku"},
		{"claude-haiku", "haiku"},
		{"OPUS", "opus"},                  // case insensitive
		{"Claude-Opus", "opus"},           // mixed case
		{"custom-model", "custom-model"},  // passthrough
	}
	for _, tt := range tests {
		got := ResolveModel(tt.input)
		if got != tt.want {
			t.Errorf("ResolveModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractTextFromStreamJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "plain text passthrough",
			input: "hello world\nfoo bar\n",
			want:  "hello world\nfoo bar\n",
		},
		{
			name: "stream-json with assistant text",
			input: `{"type":"system","subtype":"init","session_id":"abc","model":"opus"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, I found the bug."}]}}
{"type":"result","session_id":"abc"}
`,
			want: "Hello, I found the bug.",
		},
		{
			name: "multiple assistant events",
			input: `{"type":"system","subtype":"init","session_id":"abc","model":"opus"}
{"type":"assistant","message":{"content":[{"type":"text","text":"First part. "}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second part."}]}}
{"type":"result","session_id":"abc"}
`,
			want: "First part. Second part.",
		},
		{
			name: "assistant with ap-result block",
			// In real stream-json, newlines inside text are JSON-escaped.
			input: "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"abc\",\"model\":\"opus\"}\n" +
				"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Done.\\n```ap-result\\n{\\\"decision\\\":\\\"stop\\\",\\\"summary\\\":\\\"All done\\\"}\\n```\"}]}}\n" +
				"{\"type\":\"result\",\"session_id\":\"abc\"}\n",
			want: "Done.\n```ap-result\n{\"decision\":\"stop\",\"summary\":\"All done\"}\n```",
		},
		{
			name:  "malformed json lines ignored",
			input: "{bad json}\n{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n",
			want:  "ok",
		},
		{
			name: "no assistant events falls back to raw",
			input: `{"type":"system","subtype":"init","session_id":"abc","model":"opus"}
{"type":"result","session_id":"abc"}
`,
			want: "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"abc\",\"model\":\"opus\"}\n{\"type\":\"result\",\"session_id\":\"abc\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTextFromStreamJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractTextFromStreamJSON():\n  got:  %q\n  want: %q", got, tt.want)
			}
		})
	}
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
