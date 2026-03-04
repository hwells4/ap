package lock

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquire_Success(t *testing.T) {
	dir := t.TempDir()

	lk, err := Acquire(dir, "test-session")
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer lk.Release()

	if lk.Session() != "test-session" {
		t.Errorf("Session() = %q, want %q", lk.Session(), "test-session")
	}
	if lk.Path() != filepath.Join(dir, "test-session.lock") {
		t.Errorf("Path() = %q, want %q", lk.Path(), filepath.Join(dir, "test-session.lock"))
	}

	// Lock file should contain our PID.
	data, err := os.ReadFile(lk.Path())
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("parse PID %q: %v", pidStr, err)
	}
	if pid != os.Getpid() {
		t.Errorf("lock PID = %d, want %d", pid, os.Getpid())
	}
}

func TestAcquire_CreateLocksDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "locks")

	lk, err := Acquire(dir, "test-session")
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer lk.Release()

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("locks dir not created: %v", err)
	}
}

func TestAcquire_Contention(t *testing.T) {
	dir := t.TempDir()

	// Acquire first lock.
	lk1, err := Acquire(dir, "contested")
	if err != nil {
		t.Fatalf("first Acquire() error: %v", err)
	}
	defer lk1.Release()

	// Second acquire should fail with ErrLocked.
	_, err = Acquire(dir, "contested")
	if err == nil {
		t.Fatal("second Acquire() should fail")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("error = %v, want ErrLocked", err)
	}
}

func TestAcquire_DifferentSessions(t *testing.T) {
	dir := t.TempDir()

	lk1, err := Acquire(dir, "session-a")
	if err != nil {
		t.Fatalf("Acquire(session-a) error: %v", err)
	}
	defer lk1.Release()

	lk2, err := Acquire(dir, "session-b")
	if err != nil {
		t.Fatalf("Acquire(session-b) error: %v", err)
	}
	defer lk2.Release()
}

func TestRelease_Idempotent(t *testing.T) {
	dir := t.TempDir()

	lk, err := Acquire(dir, "test-release")
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}

	// First release.
	if err := lk.Release(); err != nil {
		t.Fatalf("first Release() error: %v", err)
	}

	// Second release should be safe.
	if err := lk.Release(); err != nil {
		t.Fatalf("second Release() error: %v", err)
	}
}

func TestRelease_NilLock(t *testing.T) {
	var lk *Lock
	if err := lk.Release(); err != nil {
		t.Fatalf("nil Release() error: %v", err)
	}
}

func TestRelease_AllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	lk1, err := Acquire(dir, "reacquire")
	if err != nil {
		t.Fatalf("first Acquire() error: %v", err)
	}
	if err := lk1.Release(); err != nil {
		t.Fatalf("Release() error: %v", err)
	}

	// Should be able to acquire again after release.
	lk2, err := Acquire(dir, "reacquire")
	if err != nil {
		t.Fatalf("second Acquire() error: %v", err)
	}
	defer lk2.Release()
}

func TestAcquire_StaleLockRecovery(t *testing.T) {
	dir := t.TempDir()

	// Create a lock file with a dead PID.
	// Use a high PID that's almost certainly not running.
	lockPath := filepath.Join(dir, "stale-session.lock")

	// Start a short-lived subprocess and get its PID.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	// Use PID of the now-dead process (cmd.Process.Pid).
	// Since "true" already exited, this PID should be dead.
	deadPID := cmd.Process.Pid

	// Write the dead PID to the lock file and hold a flock on it
	// from a child process that we'll kill.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("create lock file: %v", err)
	}
	if _, err := fmt.Fprintf(lockFile, "%d\n", deadPID); err != nil {
		lockFile.Close()
		t.Fatalf("write dead PID: %v", err)
	}
	lockFile.Close()

	// The lock file has the dead PID but no flock held.
	// Acquire should succeed immediately (no flock contention).
	lk, err := Acquire(dir, "stale-session")
	if err != nil {
		t.Fatalf("Acquire() on unlocked-with-dead-pid error: %v", err)
	}
	defer lk.Release()
}

func TestIsStale_NoLockFile(t *testing.T) {
	dir := t.TempDir()

	pid, err := IsStale(dir, "nonexistent")
	if err != nil {
		t.Fatalf("IsStale() error: %v", err)
	}
	if pid != 0 {
		t.Errorf("IsStale() pid = %d, want 0", pid)
	}
}

func TestIsStale_ActiveLock(t *testing.T) {
	dir := t.TempDir()

	lk, err := Acquire(dir, "active")
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer lk.Release()

	pid, err := IsStale(dir, "active")
	if err != nil {
		t.Fatalf("IsStale() error: %v", err)
	}
	// Lock held by a live process (us) — not stale.
	if pid != 0 {
		t.Errorf("IsStale() pid = %d, want 0 (active lock)", pid)
	}
}

func TestIsStale_UnlockedFile(t *testing.T) {
	dir := t.TempDir()

	// Create lock file with PID but no flock held.
	lockPath := filepath.Join(dir, "unlocked.lock")
	if err := os.WriteFile(lockPath, []byte("99999\n"), 0o644); err != nil {
		t.Fatalf("create lock file: %v", err)
	}

	pid, err := IsStale(dir, "unlocked")
	if err != nil {
		t.Fatalf("IsStale() error: %v", err)
	}
	// No flock held, so not stale (just an orphaned file).
	if pid != 0 {
		t.Errorf("IsStale() pid = %d, want 0 (no flock)", pid)
	}
}

func TestNilLock_Accessors(t *testing.T) {
	var lk *Lock

	if lk.Path() != "" {
		t.Errorf("nil Path() = %q, want empty", lk.Path())
	}
	if lk.Session() != "" {
		t.Errorf("nil Session() = %q, want empty", lk.Session())
	}
}

func TestAcquire_ContentionWithPID(t *testing.T) {
	dir := t.TempDir()

	lk, err := Acquire(dir, "pid-test")
	if err != nil {
		t.Fatalf("Acquire() error: %v", err)
	}
	defer lk.Release()

	// Second attempt should fail and error should contain our PID.
	_, err = Acquire(dir, "pid-test")
	if err == nil {
		t.Fatal("second Acquire() should fail")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Errorf("error %q should contain PID %d", err, os.Getpid())
	}
}
