package session

import "testing"

func TestResolveLauncher_Tmux(t *testing.T) {
	l, err := ResolveLauncher("tmux")
	if err != nil {
		t.Fatalf("ResolveLauncher(tmux) error = %v", err)
	}
	if l.Name() != "tmux" {
		t.Fatalf("Name() = %q, want %q", l.Name(), "tmux")
	}
}

func TestResolveLauncher_Process(t *testing.T) {
	l, err := ResolveLauncher("process")
	if err != nil {
		t.Fatalf("ResolveLauncher(process) error = %v", err)
	}
	if l.Name() != "process" {
		t.Fatalf("Name() = %q, want %q", l.Name(), "process")
	}
}

func TestResolveLauncher_EmptyDefaultsTmux(t *testing.T) {
	l, err := ResolveLauncher("")
	if err != nil {
		t.Fatalf("ResolveLauncher(\"\") error = %v", err)
	}
	if l.Name() != "tmux" {
		t.Fatalf("Name() = %q, want %q", l.Name(), "tmux")
	}
}

func TestResolveLauncher_CaseInsensitive(t *testing.T) {
	l, err := ResolveLauncher("  PROCESS  ")
	if err != nil {
		t.Fatalf("ResolveLauncher(PROCESS) error = %v", err)
	}
	if l.Name() != "process" {
		t.Fatalf("Name() = %q, want %q", l.Name(), "process")
	}
}

func TestResolveLauncher_UnknownReturnsError(t *testing.T) {
	_, err := ResolveLauncher("kubernetes")
	if err == nil {
		t.Fatal("expected error for unknown launcher")
	}
}
