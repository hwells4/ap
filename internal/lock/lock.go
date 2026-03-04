// Package lock implements flock-based per-session mutual exclusion
// with stale PID detection and deterministic error types.
//
// Each running session holds an exclusive flock on .ap/locks/{session}.lock.
// The lock file stores the holder PID for stale detection. On acquisition
// failure, the package checks whether the holding PID is still alive —
// if not, the stale lock is broken and re-acquired.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var (
	// ErrLocked is returned when a session lock is held by a live process.
	ErrLocked = errors.New("lock: session is locked")
	// ErrLockDir is returned when the locks directory cannot be created.
	ErrLockDir = errors.New("lock: cannot create locks directory")
)

// Lock represents a held session lock.
type Lock struct {
	file    *os.File
	path    string
	session string
}

// Acquire attempts to acquire an exclusive flock on the session lock file.
// locksDir is the directory containing lock files (e.g., .ap/locks).
// If the lock is held by a stale (dead) process, it is automatically broken.
func Acquire(locksDir, session string) (*Lock, error) {
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLockDir, err)
	}

	lockPath := filepath.Join(locksDir, session+".lock")

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: open %s: %w", lockPath, err)
	}

	// Try non-blocking exclusive lock.
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		// Lock acquired. Write our PID.
		if writeErr := writePID(file); writeErr != nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
			return nil, fmt.Errorf("lock: write pid: %w", writeErr)
		}
		return &Lock{file: file, path: lockPath, session: session}, nil
	}

	// Lock is held. Check if the holder is still alive.
	holderPID, readErr := readPID(file)
	if readErr != nil || holderPID <= 0 {
		// Can't read PID — assume lock is active.
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s (holder PID unknown)", ErrLocked, session)
	}

	if processAlive(holderPID) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s (held by PID %d)", ErrLocked, session, holderPID)
	}

	// Holder is dead — break the stale lock.
	// Close and reopen to reset file state.
	_ = file.Close()

	file, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("lock: reopen %s: %w", lockPath, err)
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Someone else grabbed it between our check and retry.
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s (contention during stale recovery)", ErrLocked, session)
	}

	if writeErr := writePID(file); writeErr != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("lock: write pid after stale recovery: %w", writeErr)
	}

	return &Lock{file: file, path: lockPath, session: session}, nil
}

// Release releases the session lock. Safe to call multiple times.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("lock: unlock: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("lock: close: %w", closeErr)
	}
	return nil
}

// Path returns the lock file path.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Session returns the session name this lock protects.
func (l *Lock) Session() string {
	if l == nil {
		return ""
	}
	return l.session
}

// IsStale checks if a lock file exists but is held by a dead process.
// Returns the stale PID if stale, 0 if not stale or lock doesn't exist.
func IsStale(locksDir, session string) (int, error) {
	lockPath := filepath.Join(locksDir, session+".lock")

	file, err := os.OpenFile(lockPath, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("lock: open for stale check: %w", err)
	}
	defer file.Close()

	// Try non-blocking lock to see if it's held.
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		// Not locked — release immediately.
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return 0, nil
	}

	// Locked. Read PID and check if alive.
	pid, readErr := readPID(file)
	if readErr != nil || pid <= 0 {
		return 0, nil
	}

	if !processAlive(pid) {
		return pid, nil
	}

	return 0, nil
}

// writePID truncates the file and writes the current process PID.
func writePID(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(f, "%d\n", os.Getpid())
	return err
}

// readPID reads the PID from a lock file.
func readPID(f *os.File) (int, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return 0, err
	}
	buf := make([]byte, 32)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("empty lock file")
	}
	pidStr := strings.TrimSpace(string(buf[:n]))
	return strconv.Atoi(pidStr)
}

// processAlive checks if a process with the given PID exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
