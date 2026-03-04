package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// FakeBehavior configures a fake provider binary's behavior.
type FakeBehavior struct {
	// StatusResponse is the JSON content written to the status path.
	// If empty, no status.json is written.
	StatusResponse string

	// ExitCode is the process exit code.
	ExitCode int

	// Stdout is the content written to stdout.
	Stdout string

	// Stderr is the content written to stderr.
	Stderr string

	// Delay is how long the binary waits before responding.
	Delay time.Duration
}

// FakeProviderBinResult holds the compiled fake provider binary path.
type FakeProviderBinResult struct {
	// Path is the absolute path to the compiled binary.
	Path string
}

// FakeProviderBin compiles a minimal Go binary that mimics a provider CLI:
//   - Reads prompt from stdin
//   - Reads AP_SESSION, AP_STAGE, AP_ITERATION from env
//   - Writes StatusResponse to the path from STATUS_PATH env var
//   - Prints Stdout to stdout, Stderr to stderr
//   - Exits with ExitCode
//
// The binary is compiled into t.TempDir() and auto-cleaned.
func FakeProviderBin(t *testing.T, behavior FakeBehavior) *FakeProviderBinResult {
	t.Helper()

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fakeprovider.go")
	binPath := filepath.Join(dir, "fakeprovider")

	// Generate the source code for the fake binary.
	src := generateFakeProviderSource(behavior)
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("testutil: write fake provider source: %v", err)
	}

	// Compile the binary.
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("testutil: compile fake provider: %v\n%s", err, output)
	}

	return &FakeProviderBinResult{Path: binPath}
}

func generateFakeProviderSource(b FakeBehavior) string {
	// Use a template that embeds the behavior as string literals.
	delayMs := b.Delay.Milliseconds()

	src := `package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	// Read stdin (prompt) — consume it all.
	_, _ = io.ReadAll(os.Stdin)

	// Delay if configured.
	delay := time.Duration(` + itoa64(delayMs) + `) * time.Millisecond
	if delay > 0 {
		time.Sleep(delay)
	}

	// Write stdout.
	stdout := ` + goString(b.Stdout) + `
	if stdout != "" {
		fmt.Fprint(os.Stdout, stdout)
	}

	// Write stderr.
	stderr := ` + goString(b.Stderr) + `
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}

	// Write status.json if configured.
	statusResponse := ` + goString(b.StatusResponse) + `
	statusPath := os.Getenv("STATUS_PATH")
	if statusResponse != "" && statusPath != "" {
		if err := os.WriteFile(statusPath, []byte(statusResponse), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "fake: write status: %v\n", err)
		}
	}

	os.Exit(` + itoa64(int64(b.ExitCode)) + `)
}
`
	return src
}

func goString(s string) string {
	if s == "" {
		return `""`
	}
	// Use %q to properly escape the string for Go source.
	return goQuote(s)
}

func goQuote(s string) string {
	// Simple Go string escaping using fmt.Sprintf %q.
	return "\"" + escapeGoString(s) + "\""
}

func escapeGoString(s string) string {
	var result []byte
	for _, b := range []byte(s) {
		switch b {
		case '\\':
			result = append(result, '\\', '\\')
		case '"':
			result = append(result, '\\', '"')
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		case '\t':
			result = append(result, '\\', 't')
		default:
			result = append(result, b)
		}
	}
	return string(result)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	digits := make([]byte, 0, 20)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
