package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessLauncher_Available(t *testing.T) {
	pl := NewProcessLauncher()
	if !pl.Available() {
		t.Error("ProcessLauncher should always be available")
	}
}

func TestProcessLauncher_Name(t *testing.T) {
	pl := NewProcessLauncher()
	if pl.Name() != "process" {
		t.Errorf("Name() = %q, want %q", pl.Name(), "process")
	}
}

func TestProcessLauncher_Start_Success(t *testing.T) {
	bin := buildReadyBin(t, 0)

	pl := NewProcessLauncher()
	handle, err := pl.Start("test-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer pl.Kill("test-session")

	if handle.Session != "test-session" {
		t.Errorf("Session = %q, want %q", handle.Session, "test-session")
	}
	if handle.Backend != "process" {
		t.Errorf("Backend = %q, want %q", handle.Backend, "process")
	}
	if handle.PID <= 0 {
		t.Errorf("PID = %d, want positive", handle.PID)
	}

	// Process should be running (or recently exited).
	proc, err := os.FindProcess(handle.PID)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", handle.PID, err)
	}
	_ = proc
}

func TestProcessLauncher_Start_Timeout(t *testing.T) {
	// Binary that never signals ready (sleeps forever).
	bin := buildSleepBin(t)

	pl := NewProcessLauncher()
	_, err := pl.Start("timeout-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 1,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") && err != ErrStartTimeout {
		t.Errorf("error = %v, want timeout-related error", err)
	}
}

func TestProcessLauncher_Start_DuplicateSession(t *testing.T) {
	bin := buildReadyBin(t, 0)

	pl := NewProcessLauncher()
	_, err := pl.Start("dup-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("first Start() error: %v", err)
	}
	defer pl.Kill("dup-session")

	// Second start with same session should fail.
	_, err = pl.Start("dup-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err == nil {
		t.Fatal("expected duplicate session error")
	}
	if err != ErrSessionExists {
		t.Errorf("error = %v, want ErrSessionExists", err)
	}
}

func TestProcessLauncher_Kill_Success(t *testing.T) {
	// Binary that signals ready then sleeps.
	bin := buildReadyBin(t, 60)

	pl := NewProcessLauncher()
	handle, err := pl.Start("kill-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	pid := handle.PID

	if err := pl.Kill("kill-session"); err != nil {
		t.Fatalf("Kill() error: %v", err)
	}

	// Wait briefly for process to die.
	time.Sleep(100 * time.Millisecond)

	// Process should be dead.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Error("process should be dead after Kill()")
	}
}

func TestProcessLauncher_Kill_NotFound(t *testing.T) {
	pl := NewProcessLauncher()
	err := pl.Kill("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("Kill() error = %v, want ErrSessionNotFound", err)
	}
}

func TestProcessLauncher_Kill_Idempotent(t *testing.T) {
	bin := buildReadyBin(t, 60)

	pl := NewProcessLauncher()
	_, err := pl.Start("idempotent-kill", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// First kill.
	if err := pl.Kill("idempotent-kill"); err != nil {
		t.Fatalf("first Kill() error: %v", err)
	}

	// Second kill should return not found (session removed).
	err = pl.Kill("idempotent-kill")
	if err != ErrSessionNotFound {
		t.Errorf("second Kill() error = %v, want ErrSessionNotFound", err)
	}
}

func TestProcessLauncher_Start_WithEnv(t *testing.T) {
	// Build a binary that writes an env var to a file, then signals ready.
	bin := buildEnvCheckBin(t)
	outDir := t.TempDir()
	outFile := filepath.Join(outDir, "env.txt")

	pl := NewProcessLauncher()
	_, err := pl.Start("env-session", []string{bin, outFile}, StartOptions{
		WorkDir:        outDir,
		TimeoutSeconds: 5,
		Env:            map[string]string{"TEST_VAR": "hello_from_process"},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer pl.Kill("env-session")

	// Wait for the file to be written.
	time.Sleep(200 * time.Millisecond)

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "hello_from_process" {
		t.Errorf("env value = %q, want %q", strings.TrimSpace(string(data)), "hello_from_process")
	}
}

func TestProcessLauncher_Start_ProcessExitBeforeReady(t *testing.T) {
	// Binary that exits immediately without signaling ready.
	bin := buildExitBin(t, 1)

	pl := NewProcessLauncher()
	_, err := pl.Start("exit-session", []string{bin}, StartOptions{
		WorkDir:        t.TempDir(),
		TimeoutSeconds: 5,
	})
	if err == nil {
		t.Fatal("expected error when process exits before ready")
	}
}

// buildReadyBin compiles a binary that signals ready via fd 3, then sleeps.
func buildReadyBin(t *testing.T, sleepSeconds int) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "readybin")

	code := `package main

import (
	"os"
	"time"
)

func main() {
	// Signal ready via fd 3.
	readyFD := os.NewFile(3, "ready")
	if readyFD != nil {
		readyFD.Write([]byte("ready\n"))
		readyFD.Close()
	}

	// Sleep for configured duration.
	sleepSec := ` + strconv.Itoa(sleepSeconds) + `
	if sleepSec > 0 {
		time.Sleep(time.Duration(sleepSec) * time.Second)
	}
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile readybin: %v\n%s", err, out)
	}
	return bin
}

// buildSleepBin compiles a binary that sleeps forever without signaling ready.
func buildSleepBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "sleepbin")

	code := `package main

import "time"

func main() {
	time.Sleep(24 * time.Hour)
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile sleepbin: %v\n%s", err, out)
	}
	return bin
}

// buildExitBin compiles a binary that exits immediately with the given code.
func buildExitBin(t *testing.T, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "exitbin")

	code := `package main

import "os"

func main() {
	os.Exit(` + strconv.Itoa(exitCode) + `)
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile exitbin: %v\n%s", err, out)
	}
	return bin
}

// buildEnvCheckBin compiles a binary that writes TEST_VAR env to a file,
// then signals ready via fd 3 and sleeps.
func buildEnvCheckBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "envbin")

	code := `package main

import (
	"os"
	"time"
)

func main() {
	// Write env to file (path from args[1]).
	if len(os.Args) > 1 {
		val := os.Getenv("TEST_VAR")
		os.WriteFile(os.Args[1], []byte(val+"\n"), 0o644)
	}

	// Signal ready via fd 3.
	readyFD := os.NewFile(3, "ready")
	if readyFD != nil {
		readyFD.Write([]byte("ready\n"))
		readyFD.Close()
	}

	time.Sleep(30 * time.Second)
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile envbin: %v\n%s", err, out)
	}
	return bin
}
