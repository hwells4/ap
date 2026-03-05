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

func TestLoad_InvalidLimitsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-limits.yaml")
	if err := os.WriteFile(path, []byte(`
limits:
  max_child_sessions: -1
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "limits.max_child_sessions must be >= 0") {
		t.Fatalf("expected max_child_sessions validation error, got %v", err)
	}
}

func TestLoad_TypedAccessorsExposeSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "typed-accessors.yaml")
	if err := os.WriteFile(path, []byte(`
signals:
  callback_host: "10.0.0.5"
  handler_timeout: 12s
  escalate:
    - type: stdout
  spawn:
    - type: exec
      argv: ["echo", "spawned"]
limits:
  max_child_sessions: 17
  max_spawn_depth: 6
hooks:
  on_completed: "echo done"
  on_escalate: "echo escalate"
  on_idle: "echo idle"
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	limits := cfg.RunnerLimits()
	if limits.MaxChildSessions != 17 {
		t.Fatalf("RunnerLimits().MaxChildSessions = %d, want 17", limits.MaxChildSessions)
	}
	if limits.MaxSpawnDepth != 6 {
		t.Fatalf("RunnerLimits().MaxSpawnDepth = %d, want 6", limits.MaxSpawnDepth)
	}

	escalateHandlers := cfg.SignalHandlers("escalate")
	if len(escalateHandlers) != 1 || escalateHandlers[0].Type != "stdout" {
		t.Fatalf("SignalHandlers(escalate) = %#v, want one stdout handler", escalateHandlers)
	}
	spawnHandlers := cfg.SignalHandlers("spawn")
	if len(spawnHandlers) != 1 || spawnHandlers[0].Type != "exec" {
		t.Fatalf("SignalHandlers(spawn) = %#v, want one exec handler", spawnHandlers)
	}
	if got := cfg.SignalHandlers("unknown-signal"); len(got) != 0 {
		t.Fatalf("SignalHandlers(unknown-signal) = %#v, want empty", got)
	}

	// Ensure SignalHandlers returns a copy and callers cannot mutate config internals.
	escalateHandlers[0].Type = "mutated"
	if cfg.SignalHandlers("escalate")[0].Type != "stdout" {
		t.Fatalf("SignalHandlers should return a copy, got mutated value %q", cfg.SignalHandlers("escalate")[0].Type)
	}

	hooks := cfg.WatchHooks()
	if hooks.OnCompleted != "echo done" {
		t.Fatalf("WatchHooks().OnCompleted = %q, want %q", hooks.OnCompleted, "echo done")
	}
	if hooks.OnEscalate != "echo escalate" {
		t.Fatalf("WatchHooks().OnEscalate = %q, want %q", hooks.OnEscalate, "echo escalate")
	}
	if hooks.OnIdle != "echo idle" {
		t.Fatalf("WatchHooks().OnIdle = %q, want %q", hooks.OnIdle, "echo idle")
	}
}

func TestLoad_DefaultsSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "with-defaults.yaml")
	if err := os.WriteFile(path, []byte(`
defaults:
  launcher: process
  provider: codex
  model: codex-mini-latest
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DefaultLauncher() != "process" {
		t.Fatalf("DefaultLauncher() = %q, want %q", cfg.DefaultLauncher(), "process")
	}
	if cfg.DefaultProvider() != "codex" {
		t.Fatalf("DefaultProvider() = %q, want %q", cfg.DefaultProvider(), "codex")
	}
	if cfg.DefaultModel() != "codex-mini-latest" {
		t.Fatalf("DefaultModel() = %q, want %q", cfg.DefaultModel(), "codex-mini-latest")
	}
}

func TestLoad_DefaultsAppliedWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error = %v", err)
	}

	if cfg.DefaultLauncher() != "tmux" {
		t.Fatalf("DefaultLauncher() = %q, want %q", cfg.DefaultLauncher(), "tmux")
	}
	if cfg.DefaultProvider() != "claude" {
		t.Fatalf("DefaultProvider() = %q, want %q", cfg.DefaultProvider(), "claude")
	}
	if cfg.DefaultModel() != "" {
		t.Fatalf("DefaultModel() = %q, want %q", cfg.DefaultModel(), "")
	}
}

func TestValidate_InvalidLauncherName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-launcher.yaml")
	if err := os.WriteFile(path, []byte(`
defaults:
  launcher: kubernetes
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid launcher")
	}
	if !strings.Contains(err.Error(), "defaults.launcher") {
		t.Fatalf("expected defaults.launcher validation error, got %v", err)
	}
}

func TestNormalize_LowercasesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upper-defaults.yaml")
	if err := os.WriteFile(path, []byte(`
defaults:
  launcher: TMUX
  provider: Claude
`), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DefaultLauncher() != "tmux" {
		t.Fatalf("DefaultLauncher() = %q, want %q", cfg.DefaultLauncher(), "tmux")
	}
	if cfg.DefaultProvider() != "claude" {
		t.Fatalf("DefaultProvider() = %q, want %q", cfg.DefaultProvider(), "claude")
	}
}
