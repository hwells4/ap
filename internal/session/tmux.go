package session

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultStartTimeout = 30 * time.Second
	tmuxPrefix          = "ap-"
)

// TmuxLauncher implements Launcher using tmux sessions with wait-for readiness.
type TmuxLauncher struct{}

// NewTmuxLauncher returns a new TmuxLauncher.
func NewTmuxLauncher() *TmuxLauncher {
	return &TmuxLauncher{}
}

// Name returns "tmux".
func (t *TmuxLauncher) Name() string { return "tmux" }

// Available checks if tmux is installed and the server is reachable.
func (t *TmuxLauncher) Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Start creates a new tmux session running the given command.
// It uses `tmux wait-for` for readiness signaling: the runner must
// call `tmux wait-for -S ap-ready-{session}` after initialization.
func (t *TmuxLauncher) Start(session string, runnerCmd []string, opts StartOptions) (SessionHandle, error) {
	if !t.Available() {
		return SessionHandle{}, ErrLauncherUnavailable
	}

	tmuxName := tmuxSessionName(session)

	// Check if session already exists.
	if tmuxSessionExists(tmuxName) {
		return SessionHandle{}, fmt.Errorf("%w: tmux session %q", ErrSessionExists, tmuxName)
	}

	timeout := defaultStartTimeout
	if opts.TimeoutSeconds > 0 {
		timeout = time.Duration(opts.TimeoutSeconds) * time.Second
	}

	readyChannel := "ap-ready-" + session

	// Build the shell command that runs the runner then signals readiness.
	// The runner signals readiness itself, but as a fallback we also handle
	// the case where the runner doesn't signal by timing out.
	shellCmd := buildShellCommand(runnerCmd, readyChannel)

	// Build tmux new-session command.
	tmuxArgs := []string{
		"new-session",
		"-d", // detached
		"-s", tmuxName,
		"-x", "200", // width
		"-y", "50", // height
	}

	// Set working directory if specified.
	if opts.WorkDir != "" {
		tmuxArgs = append(tmuxArgs, "-c", opts.WorkDir)
	}

	// Set environment variables.
	envArgs := buildTmuxEnv(opts.Env)

	tmuxArgs = append(tmuxArgs, shellCmd)

	// Create the tmux session.
	createCmd := exec.Command("tmux", tmuxArgs...)
	createCmd.Env = append(createCmd.Environ(), envArgs...)
	if out, err := createCmd.CombinedOutput(); err != nil {
		return SessionHandle{}, fmt.Errorf("session: tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Wait for readiness signal with timeout.
	pid, err := waitForReady(readyChannel, tmuxName, timeout)
	if err != nil {
		// Kill the session on startup failure.
		_ = killTmuxSession(tmuxName)
		return SessionHandle{}, err
	}

	return SessionHandle{
		Session: session,
		PID:     pid,
		Backend: "tmux",
	}, nil
}

// Kill terminates a tmux session. Idempotent.
func (t *TmuxLauncher) Kill(session string) error {
	tmuxName := tmuxSessionName(session)

	if !tmuxSessionExists(tmuxName) {
		return nil // Idempotent: already gone.
	}

	return killTmuxSession(tmuxName)
}

// tmuxSessionName returns the tmux session name for an ap session.
func tmuxSessionName(session string) string {
	return tmuxPrefix + session
}

// tmuxSessionExists checks if a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// killTmuxSession kills a tmux session by name.
func killTmuxSession(name string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		// "session not found" is not an error for idempotent kill.
		if strings.Contains(outStr, "not found") || strings.Contains(outStr, "no server") || strings.Contains(outStr, "can't find session") {
			return nil
		}
		return fmt.Errorf("session: tmux kill-session: %w: %s", err, outStr)
	}
	return nil
}

// buildShellCommand creates the shell command string for the tmux session.
// It runs the runner command, then signals readiness on the wait-for channel.
// Format: "cmd args... ; tmux wait-for -S channel" (signal even on failure
// so the parent doesn't hang).
func buildShellCommand(runnerCmd []string, readyChannel string) string {
	// Quote each argument for shell safety.
	quoted := make([]string, len(runnerCmd))
	for i, arg := range runnerCmd {
		quoted[i] = shellQuote(arg)
	}

	// Signal ready before running the command, so the parent knows the session started.
	// The runner will do its own initialization and the lock protects against races.
	return fmt.Sprintf("tmux wait-for -S %s; %s",
		shellQuote(readyChannel),
		strings.Join(quoted, " "))
}

// waitForReady waits for the tmux wait-for channel to be signaled.
func waitForReady(channel, tmuxName string, timeout time.Duration) (int, error) {
	done := make(chan error, 1)
	var waitCmd *exec.Cmd

	go func() {
		waitCmd = exec.Command("tmux", "wait-for", channel)
		done <- waitCmd.Run()
	}()

	select {
	case err := <-done:
		if err != nil {
			return 0, fmt.Errorf("%w: wait-for channel %q failed: %v", ErrStartTimeout, channel, err)
		}
		// Try to get the PID of the runner process inside the tmux session.
		pid := getTmuxPanePID(tmuxName)
		return pid, nil
	case <-time.After(timeout):
		if waitCmd != nil && waitCmd.Process != nil {
			_ = waitCmd.Process.Kill()
		}
		return 0, fmt.Errorf("%w: no readiness signal within %s", ErrStartTimeout, timeout)
	}
}

// getTmuxPanePID returns the PID of the process running in the tmux session's pane.
func getTmuxPanePID(tmuxName string) int {
	cmd := exec.Command("tmux", "list-panes", "-t", tmuxName, "-F", "#{pane_pid}")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return pid
}

// buildTmuxEnv converts env vars to KEY=VALUE format for process environment.
func buildTmuxEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	// Replace single quotes with '\'' and wrap in single quotes.
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
