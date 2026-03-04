package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeProviderBin_Success(t *testing.T) {
	statusJSON := `{"decision":"continue","summary":"did work","work":{"items_completed":[],"files_touched":[]},"errors":[]}`
	bin := FakeProviderBin(t, FakeBehavior{
		StatusResponse: statusJSON,
		ExitCode:       0,
		Stdout:         "hello stdout",
		Stderr:         "hello stderr",
	})

	if _, err := os.Stat(bin.Path); err != nil {
		t.Fatalf("binary not found: %v", err)
	}

	// Run the binary with STATUS_PATH env var.
	statusPath := filepath.Join(t.TempDir(), "status.json")
	cmd := exec.Command(bin.Path)
	cmd.Stdin = strings.NewReader("test prompt")
	cmd.Env = append(os.Environ(),
		"STATUS_PATH="+statusPath,
		"AP_SESSION=test-session",
		"AP_STAGE=ralph",
		"AP_ITERATION=1",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := stdout.String(); got != "hello stdout" {
		t.Errorf("stdout = %q, want %q", got, "hello stdout")
	}
	if got := stderr.String(); got != "hello stderr" {
		t.Errorf("stderr = %q, want %q", got, "hello stderr")
	}

	// Verify status.json was written.
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status.json: %v", err)
	}
	if !strings.Contains(string(data), `"continue"`) {
		t.Errorf("status.json does not contain expected decision: %s", data)
	}
}

func TestFakeProviderBin_ExitCode(t *testing.T) {
	bin := FakeProviderBin(t, FakeBehavior{
		ExitCode: 42,
	})

	cmd := exec.Command(bin.Path)
	cmd.Stdin = strings.NewReader("test")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 42 {
		t.Errorf("exit code = %d, want 42", exitErr.ExitCode())
	}
}

func TestFakeProviderBin_NoStatus(t *testing.T) {
	bin := FakeProviderBin(t, FakeBehavior{
		ExitCode: 0,
		// No StatusResponse → no status.json written.
	})

	statusPath := filepath.Join(t.TempDir(), "status.json")
	cmd := exec.Command(bin.Path)
	cmd.Stdin = strings.NewReader("test")
	cmd.Env = append(os.Environ(), "STATUS_PATH="+statusPath)

	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if _, err := os.Stat(statusPath); !os.IsNotExist(err) {
		t.Error("status.json should not exist when StatusResponse is empty")
	}
}

func TestFakeProviderBin_Delay(t *testing.T) {
	bin := FakeProviderBin(t, FakeBehavior{
		ExitCode: 0,
		Delay:    200 * time.Millisecond,
	})

	cmd := exec.Command(bin.Path)
	cmd.Stdin = strings.NewReader("test")

	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 150ms (configured 200ms delay)", elapsed)
	}
}

func TestFakeProviderBin_ReadsStdin(t *testing.T) {
	// Verify the binary consumes stdin without hanging.
	bin := FakeProviderBin(t, FakeBehavior{ExitCode: 0})

	cmd := exec.Command(bin.Path)
	cmd.Stdin = strings.NewReader("this is the prompt content\nwith multiple lines\n")

	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}
