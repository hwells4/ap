package session

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const readySignal = "ready"

// processEntry tracks a running session process.
type processEntry struct {
	pid     int
	session string
}

// ProcessLauncher implements Launcher using detached OS processes
// with pipe-based readiness signaling via ExtraFiles (fd 3).
type ProcessLauncher struct {
	mu       sync.Mutex
	sessions map[string]processEntry
}

// NewProcessLauncher creates a new ProcessLauncher.
func NewProcessLauncher() *ProcessLauncher {
	return &ProcessLauncher{
		sessions: make(map[string]processEntry),
	}
}

// Available always returns true — ProcessLauncher is the universal fallback.
func (p *ProcessLauncher) Available() bool {
	return true
}

// Name returns "process".
func (p *ProcessLauncher) Name() string {
	return "process"
}

// Start launches a runner process for the given session. The child signals
// readiness by writing "ready\n" to fd 3 (passed via ExtraFiles). The parent
// blocks until readiness is received or the timeout expires.
func (p *ProcessLauncher) Start(session string, runnerCmd []string, opts StartOptions) (SessionHandle, error) {
	p.mu.Lock()
	if _, exists := p.sessions[session]; exists {
		p.mu.Unlock()
		return SessionHandle{}, ErrSessionExists
	}
	p.mu.Unlock()

	if len(runnerCmd) == 0 {
		return SessionHandle{}, fmt.Errorf("session: empty runner command")
	}

	timeout := defaultStartTimeout
	if opts.TimeoutSeconds > 0 {
		timeout = time.Duration(opts.TimeoutSeconds) * time.Second
	}

	// Create readiness pipe: child writes to w (fd 3), parent reads from r.
	r, w, err := os.Pipe()
	if err != nil {
		return SessionHandle{}, fmt.Errorf("session: create readiness pipe: %w", err)
	}

	cmd := exec.Command(runnerCmd[0], runnerCmd[1:]...)
	cmd.Dir = opts.WorkDir
	cmd.Env = buildEnv(opts.Env)

	// Pass write end as fd 3 via ExtraFiles.
	cmd.ExtraFiles = []*os.File{w}

	// Set up process group so we can kill the whole group.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Start the process.
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return SessionHandle{}, fmt.Errorf("session: start process: %w", err)
	}

	// Close write end in parent — only child should write.
	w.Close()

	pid := cmd.Process.Pid

	// Wait for readiness signal or timeout.
	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		if scanner.Scan() && scanner.Text() == readySignal {
			readyCh <- nil
		} else {
			readyCh <- fmt.Errorf("session: no ready signal from child")
		}
		r.Close()
	}()

	// Also monitor for early exit.
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			// Process didn't signal ready. Try to clean up.
			killProcess(pid)
			return SessionHandle{}, err
		}
	case exitErr := <-exitCh:
		// Process exited. Check if ready signal was buffered before exit.
		select {
		case err := <-readyCh:
			if err == nil {
				// Process signaled ready then exited quickly. That's valid.
				break
			}
			// Ready signal failed and process exited.
			if exitErr != nil {
				return SessionHandle{}, fmt.Errorf("session: process exited before ready: %w", exitErr)
			}
			return SessionHandle{}, fmt.Errorf("session: process exited before ready (exit 0)")
		case <-time.After(100 * time.Millisecond):
			r.Close()
			if exitErr != nil {
				return SessionHandle{}, fmt.Errorf("session: process exited before ready: %w", exitErr)
			}
			return SessionHandle{}, fmt.Errorf("session: process exited before ready (exit 0)")
		}
	case <-time.After(timeout):
		r.Close()
		killProcess(pid)
		return SessionHandle{}, ErrStartTimeout
	}

	// Register session.
	p.mu.Lock()
	p.sessions[session] = processEntry{pid: pid, session: session}
	p.mu.Unlock()

	return SessionHandle{
		Session: session,
		PID:     pid,
		Backend: "process",
	}, nil
}

// Kill terminates a running session process. Returns ErrSessionNotFound if
// no such session exists. Idempotent: killing an already-dead session
// removes the tracking entry.
func (p *ProcessLauncher) Kill(session string) error {
	p.mu.Lock()
	entry, exists := p.sessions[session]
	if !exists {
		p.mu.Unlock()
		return ErrSessionNotFound
	}
	delete(p.sessions, session)
	p.mu.Unlock()

	killProcess(entry.pid)
	return nil
}

// killProcess sends SIGTERM to the process group, then SIGKILL after a brief wait.
func killProcess(pid int) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// SIGTERM the process group.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Brief grace period, then SIGKILL.
	time.AfterFunc(2*time.Second, func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
}

// buildEnv creates the environment for the child process, merging
// the parent's environment with any additional variables.
func buildEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
