package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseBeads_ReleasesInProgressBeads(t *testing.T) {
	// Create a fake bd script that records calls.
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "bd-calls.log")
	fakeBD := filepath.Join(tmpDir, "bd")

	script := `#!/bin/sh
echo "$@" >> ` + logFile + `
if echo "$@" | grep -q "list"; then
  echo "ap-001"
  echo "ap-002"
fi
`
	if err := os.WriteFile(fakeBD, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	err := ReleaseBeads("test-session", fakeBD)
	if err != nil {
		t.Fatalf("ReleaseBeads() error: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Should have 3 calls: 1 list + 2 updates.
	if len(lines) != 3 {
		t.Fatalf("bd calls = %d, want 3; calls:\n%s", len(lines), string(data))
	}

	if !strings.Contains(lines[0], "list") || !strings.Contains(lines[0], "pipeline/test-session") {
		t.Fatalf("first call should be list with label, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "update ap-001 --status=open") {
		t.Fatalf("second call should update ap-001, got: %s", lines[1])
	}
	if !strings.Contains(lines[2], "update ap-002 --status=open") {
		t.Fatalf("third call should update ap-002, got: %s", lines[2])
	}
}

func TestReleaseBeads_NoBeadsIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "bd-calls.log")
	fakeBD := filepath.Join(tmpDir, "bd")

	// bd list returns nothing.
	script := `#!/bin/sh
echo "$@" >> ` + logFile + `
`
	if err := os.WriteFile(fakeBD, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	err := ReleaseBeads("empty-session", fakeBD)
	if err != nil {
		t.Fatalf("ReleaseBeads() error: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Only the list call, no updates.
	if len(lines) != 1 {
		t.Fatalf("bd calls = %d, want 1; calls:\n%s", len(lines), string(data))
	}
}

func TestReleaseBeads_IdempotentOnMissingBD(t *testing.T) {
	// When bd is not found, ReleaseBeads should not error.
	err := ReleaseBeads("whatever", "/nonexistent/bd")
	if err != nil {
		t.Fatalf("ReleaseBeads() error: %v, want nil (idempotent)", err)
	}
}

func TestReleaseBeads_BDFailureIsNotFatal(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBD := filepath.Join(tmpDir, "bd")

	// bd list fails with exit code 1.
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeBD, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	err := ReleaseBeads("fail-session", fakeBD)
	if err != nil {
		t.Fatalf("ReleaseBeads() error: %v, want nil (best-effort)", err)
	}
}
