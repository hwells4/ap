package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_DefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".config", "ap", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
signals:
  callback_host: "100.95.25.7"
  handler_timeout: 45s
  escalate:
    - type: webhook
      url: http://example.com/signals
limits:
  max_child_sessions: 25
  max_spawn_depth: 7
hooks:
  on_completed: "notify-send done"
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v", err)
	}

	if cfg.Signals.CallbackHost != "100.95.25.7" {
		t.Fatalf("CallbackHost = %q, want %q", cfg.Signals.CallbackHost, "100.95.25.7")
	}
	if cfg.Signals.HandlerTimeout != 45*time.Second {
		t.Fatalf("HandlerTimeout = %v, want 45s", cfg.Signals.HandlerTimeout)
	}
	if cfg.Limits.MaxChildSessions != 25 {
		t.Fatalf("MaxChildSessions = %d, want 25", cfg.Limits.MaxChildSessions)
	}
	if cfg.Limits.MaxSpawnDepth != 7 {
		t.Fatalf("MaxSpawnDepth = %d, want 7", cfg.Limits.MaxSpawnDepth)
	}
	if cfg.Hooks.OnCompleted != "notify-send done" {
		t.Fatalf("OnCompleted = %q, want %q", cfg.Hooks.OnCompleted, "notify-send done")
	}

	handlers := cfg.SignalHandlers("escalate")
	if len(handlers) != 1 || handlers[0].Type != "webhook" {
		t.Fatalf("escalate handlers mismatch: %#v", handlers)
	}
}

func TestLoad_ExplicitOverridePath(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(overridePath, []byte(`
signals:
  spawn:
    - type: stdout
hooks:
  on_idle: "echo idle"
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(overridePath)
	if err != nil {
		t.Fatalf("Load(override) error = %v", err)
	}

	if len(cfg.SignalHandlers("spawn")) != 1 {
		t.Fatalf("spawn handlers mismatch: %#v", cfg.SignalHandlers("spawn"))
	}
	if cfg.WatchHooks().OnIdle != "echo idle" {
		t.Fatalf("OnIdle = %q, want %q", cfg.WatchHooks().OnIdle, "echo idle")
	}
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v", err)
	}

	if cfg.Signals.CallbackHost != defaultCallbackHost {
		t.Fatalf("CallbackHost = %q, want %q", cfg.Signals.CallbackHost, defaultCallbackHost)
	}
	if cfg.Limits.MaxChildSessions != defaultMaxChildSessions {
		t.Fatalf("MaxChildSessions = %d, want %d", cfg.Limits.MaxChildSessions, defaultMaxChildSessions)
	}
	if cfg.Limits.MaxSpawnDepth != defaultMaxSpawnDepth {
		t.Fatalf("MaxSpawnDepth = %d, want %d", cfg.Limits.MaxSpawnDepth, defaultMaxSpawnDepth)
	}
}

func TestLoad_UnknownFieldError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte(`
signals:
  callback_host: "127.0.0.1"
  mystery: true
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoad_InvalidHandlerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-handler.yaml")
	if err := os.WriteFile(path, []byte(`
signals:
  escalate:
    - type: webhook
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "signals.escalate[0].url is required") {
		t.Fatalf("expected webhook url validation error, got %v", err)
	}
}
