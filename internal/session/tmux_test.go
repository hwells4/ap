package session

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func skipWithoutTmux(t *testing.T) {
	t.Helper()
	if !tmuxAvailable() {
		t.Skip("tmux not available")
	}
}

// cleanupTmuxSession kills a tmux session if it exists. Test helper.
func cleanupTmuxSession(t *testing.T, session string) {
	t.Helper()
	name := tmuxSessionName(session)
	_ = killTmuxSession(name)
}

// --- Unit tests (no tmux required) ---

func TestTmuxSessionName(t *testing.T) {
	cases := []struct {
		session string
		want    string
	}{
		{"my-session", "ap-my-session"},
		{"test", "ap-test"},
		{"x", "ap-x"},
	}
	for _, tc := range cases {
		got := tmuxSessionName(tc.session)
		if got != tc.want {
			t.Errorf("tmuxSessionName(%q) = %q, want %q", tc.session, got, tc.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"$HOME", "'$HOME'"},
		{`"quoted"`, `'"quoted"'`},
	}
	for _, tc := range cases {
		got := shellQuote(tc.input)
		if got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBuildShellCommand(t *testing.T) {
	cmd := buildShellCommand([]string{"ap", "_run", "--session", "test"}, "ap-ready-test")
	// Should signal ready first, then run the command.
	if !strings.Contains(cmd, "tmux wait-for -S") {
		t.Errorf("missing wait-for signal in command: %s", cmd)
	}
	if !strings.Contains(cmd, "'ap'") {
		t.Errorf("missing quoted 'ap' in command: %s", cmd)
	}
	if !strings.Contains(cmd, "'_run'") {
		t.Errorf("missing quoted '_run' in command: %s", cmd)
	}
}

func TestBuildTmuxEnv(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := buildTmuxEnv(nil); got != nil {
			t.Errorf("buildTmuxEnv(nil) = %v, want nil", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if got := buildTmuxEnv(map[string]string{}); got != nil {
			t.Errorf("buildTmuxEnv(empty) = %v, want nil", got)
		}
	})

	t.Run("values", func(t *testing.T) {
		env := buildTmuxEnv(map[string]string{"A": "1", "B": "2"})
		if len(env) != 2 {
			t.Fatalf("len = %d, want 2", len(env))
		}
		joined := strings.Join(env, ",")
		if !strings.Contains(joined, "A=1") {
			t.Error("missing A=1")
		}
		if !strings.Contains(joined, "B=2") {
			t.Error("missing B=2")
		}
	})
}

func TestTmuxLauncher_Name(t *testing.T) {
	tl := NewTmuxLauncher()
	if tl.Name() != "tmux" {
		t.Errorf("Name() = %q, want %q", tl.Name(), "tmux")
	}
}

func TestTmuxLauncher_Available(t *testing.T) {
	tl := NewTmuxLauncher()
	got := tl.Available()
	want := tmuxAvailable()
	if got != want {
		t.Errorf("Available() = %v, want %v", got, want)
	}
}

// --- Integration tests (require tmux) ---

func TestIntegration_TmuxLauncher_StartAndKill(t *testing.T) {
	skipWithoutTmux(t)

	session := "tmux-test-start-" + time.Now().Format("150405")
	defer cleanupTmuxSession(t, session)

	tl := NewTmuxLauncher()

	// Start a session running a simple command that signals ready immediately.
	// buildShellCommand signals ready before running the command, so even
	// a long-running command will signal ready.
	handle, err := tl.Start(session, []string{"sleep", "300"}, StartOptions{
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if handle.Session != session {
		t.Errorf("Session = %q, want %q", handle.Session, session)
	}
	if handle.Backend != "tmux" {
		t.Errorf("Backend = %q, want %q", handle.Backend, "tmux")
	}

	// Tmux session should exist.
	tmuxName := tmuxSessionName(session)
	if !tmuxSessionExists(tmuxName) {
		t.Error("tmux session should exist after Start()")
	}

	// Kill the session.
	if err := tl.Kill(session); err != nil {
		t.Fatalf("Kill() error: %v", err)
	}

	// Give tmux a moment to clean up.
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists(tmuxName) {
		t.Error("tmux session should not exist after Kill()")
	}
}

func TestIntegration_TmuxLauncher_DuplicateSession(t *testing.T) {
	skipWithoutTmux(t)

	session := "tmux-test-dup-" + time.Now().Format("150405")
	defer cleanupTmuxSession(t, session)

	tl := NewTmuxLauncher()

	_, err := tl.Start(session, []string{"sleep", "300"}, StartOptions{
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("first Start() error: %v", err)
	}

	// Second start with same name should fail.
	_, err = tl.Start(session, []string{"sleep", "300"}, StartOptions{
		TimeoutSeconds: 5,
	})
	if err == nil {
		t.Fatal("expected error for duplicate session")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want 'already exists' error", err)
	}
}

func TestIntegration_TmuxLauncher_KillIdempotent(t *testing.T) {
	skipWithoutTmux(t)

	session := "tmux-test-kill-idem-" + time.Now().Format("150405")

	tl := NewTmuxLauncher()

	// Kill a session that doesn't exist — should be idempotent (no error).
	if err := tl.Kill(session); err != nil {
		t.Errorf("Kill() on nonexistent session: %v", err)
	}

	// Start, then kill twice.
	_, err := tl.Start(session, []string{"sleep", "300"}, StartOptions{
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if err := tl.Kill(session); err != nil {
		t.Fatalf("first Kill() error: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Second kill should also succeed (idempotent).
	if err := tl.Kill(session); err != nil {
		t.Errorf("second Kill() error: %v", err)
	}
}

func TestIntegration_TmuxLauncher_HandleFields(t *testing.T) {
	skipWithoutTmux(t)

	session := "tmux-test-handle-" + time.Now().Format("150405")
	defer cleanupTmuxSession(t, session)

	tl := NewTmuxLauncher()
	handle, err := tl.Start(session, []string{"sleep", "300"}, StartOptions{
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if handle.PID <= 0 {
		t.Errorf("PID = %d, want positive", handle.PID)
	}
	if handle.Backend != "tmux" {
		t.Errorf("Backend = %q, want %q", handle.Backend, "tmux")
	}
	if handle.Session != session {
		t.Errorf("Session = %q, want %q", handle.Session, session)
	}
}

func TestIntegration_TmuxLauncher_WorkDir(t *testing.T) {
	skipWithoutTmux(t)

	session := "tmux-test-workdir-" + time.Now().Format("150405")
	defer cleanupTmuxSession(t, session)

	workDir := t.TempDir()

	tl := NewTmuxLauncher()
	_, err := tl.Start(session, []string{"sleep", "300"}, StartOptions{
		WorkDir:        workDir,
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Verify the tmux pane's working directory.
	tmuxName := tmuxSessionName(session)
	cmd := exec.Command("tmux", "list-panes", "-t", tmuxName, "-F", "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list-panes error: %v", err)
	}

	got := strings.TrimSpace(string(out))
	if got != workDir {
		t.Errorf("pane working dir = %q, want %q", got, workDir)
	}
}

func TestIntegration_KillTmuxSession_NotFound(t *testing.T) {
	skipWithoutTmux(t)

	// killTmuxSession on a non-existent session should return nil (idempotent).
	err := killTmuxSession("ap-nonexistent-" + time.Now().Format("150405"))
	if err != nil {
		t.Errorf("killTmuxSession(nonexistent) = %v, want nil", err)
	}
}

func TestIntegration_TmuxSessionExists(t *testing.T) {
	skipWithoutTmux(t)

	name := "ap-exists-test-" + time.Now().Format("150405")

	// Should not exist initially.
	if tmuxSessionExists(name) {
		t.Fatal("session should not exist before creation")
	}

	// Create it directly via tmux.
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "sleep", "300")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create tmux session: %v: %s", err, out)
	}
	defer func() { _ = killTmuxSession(name) }()

	if !tmuxSessionExists(name) {
		t.Error("session should exist after creation")
	}

	// Kill and verify.
	_ = killTmuxSession(name)
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists(name) {
		t.Error("session should not exist after kill")
	}
}
