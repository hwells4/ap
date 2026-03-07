// Package config loads typed user configuration from ~/.config/ap/config.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultCallbackHost          = "127.0.0.1"
	defaultMaxChildSessions      = 10
	defaultMaxSpawnDepth         = 3
	defaultHandlerTimeout        = 30 * time.Second
	defaultMaxConcurrentProvider = 0 // 0 means unlimited
)

// Config is the typed representation of config.yaml.
type Config struct {
	Defaults DefaultsConfig `yaml:"defaults"`
	Signals  SignalsConfig  `yaml:"signals"`
	Limits   LimitsConfig   `yaml:"limits"`
	Hooks    HooksConfig    `yaml:"hooks"`
}

// DefaultsConfig sets default values for launcher, provider, and model.
// Precedence: CLI flag > AP_* env var > config defaults > compiled default.
type DefaultsConfig struct {
	Launcher string `yaml:"launcher"`
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// SignalsConfig configures signal dispatch behavior.
type SignalsConfig struct {
	CallbackHost   string          `yaml:"callback_host"`
	HandlerTimeout time.Duration   `yaml:"handler_timeout"`
	Escalate       []SignalHandler `yaml:"escalate"`
	Spawn          []SignalHandler `yaml:"spawn"`
}

// SignalHandler configures one signal handler in the dispatch chain.
type SignalHandler struct {
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Argv    []string          `yaml:"argv,omitempty"`
}

// LimitsConfig defines operational limits used by the runner.
type LimitsConfig struct {
	MaxChildSessions       int `yaml:"max_child_sessions"`
	MaxSpawnDepth          int `yaml:"max_spawn_depth"`
	MaxConcurrentProviders int `yaml:"max_concurrent_providers"`
}

// HooksConfig defines shell hooks consumed by watch/event features and the runner.
type HooksConfig struct {
	// Watch hooks (consumed by ap watch).
	OnCompleted string `yaml:"on_completed,omitempty"`
	OnEscalate  string `yaml:"on_escalate,omitempty"`
	OnIdle      string `yaml:"on_idle,omitempty"`

	// Runner lifecycle hooks.
	PreSession    string        `yaml:"pre_session,omitempty"`
	PreIteration  string        `yaml:"pre_iteration,omitempty"`
	PreStage      string        `yaml:"pre_stage,omitempty"`
	PostIteration string        `yaml:"post_iteration,omitempty"`
	PostStage     string        `yaml:"post_stage,omitempty"`
	PostSession   string        `yaml:"post_session,omitempty"`
	OnFailure     string        `yaml:"on_failure,omitempty"`
	Timeout       time.Duration `yaml:"timeout,omitempty"`
}

// Default returns a config with all default values applied.
func Default() Config {
	return Config{
		Defaults: DefaultsConfig{
			Launcher: "tmux",
			Provider: "claude",
			Model:    "",
		},
		Signals: SignalsConfig{
			CallbackHost:   defaultCallbackHost,
			HandlerTimeout: defaultHandlerTimeout,
			Escalate:       []SignalHandler{},
			Spawn:          []SignalHandler{},
		},
		Limits: LimitsConfig{
			MaxChildSessions:       defaultMaxChildSessions,
			MaxSpawnDepth:          defaultMaxSpawnDepth,
			MaxConcurrentProviders: defaultMaxConcurrentProvider,
		},
		Hooks: HooksConfig{
			Timeout: 60 * time.Second,
		},
	}
}

// DefaultPath returns ~/.config/ap/config.yaml for the current user.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ap", "config.yaml"), nil
}

// Load reads config from path or the default path when path is empty.
// Missing config files are treated as "defaults only".
func Load(path string) (Config, error) {
	config := Default()

	resolvedPath := strings.TrimSpace(path)
	if resolvedPath == "" {
		var err error
		resolvedPath, err = DefaultPath()
		if err != nil {
			return Config{}, err
		}
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", resolvedPath, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", resolvedPath, err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", resolvedPath, err)
	}
	config.normalize()
	return config, nil
}

// Validate verifies config values and required fields.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}

	if launcher := strings.ToLower(strings.TrimSpace(c.Defaults.Launcher)); launcher != "" {
		switch launcher {
		case "tmux", "process":
		default:
			return fmt.Errorf("defaults.launcher %q is invalid (allowed: tmux, process)", c.Defaults.Launcher)
		}
	}

	if c.Signals.HandlerTimeout < 0 {
		return fmt.Errorf("signals.handler_timeout must be >= 0")
	}
	if c.Limits.MaxChildSessions < 0 {
		return fmt.Errorf("limits.max_child_sessions must be >= 0")
	}
	if c.Limits.MaxSpawnDepth < 0 {
		return fmt.Errorf("limits.max_spawn_depth must be >= 0")
	}
	if c.Limits.MaxConcurrentProviders < 0 {
		return fmt.Errorf("limits.max_concurrent_providers must be >= 0")
	}

	if err := validateHandlers("signals.escalate", c.Signals.Escalate); err != nil {
		return err
	}
	if err := validateHandlers("signals.spawn", c.Signals.Spawn); err != nil {
		return err
	}
	return nil
}

// RunnerLimits returns typed limit settings for runner/session logic.
func (c Config) RunnerLimits() LimitsConfig {
	return c.Limits
}

// SignalHandlers returns the configured handler chain for a signal type.
func (c Config) SignalHandlers(signalType string) []SignalHandler {
	switch strings.ToLower(strings.TrimSpace(signalType)) {
	case "escalate":
		return append([]SignalHandler(nil), c.Signals.Escalate...)
	case "spawn":
		return append([]SignalHandler(nil), c.Signals.Spawn...)
	default:
		return []SignalHandler{}
	}
}

// WatchHooks returns typed watch hook commands.
func (c Config) WatchHooks() HooksConfig {
	return c.Hooks
}

// DefaultLauncher returns the configured default launcher backend name.
func (c Config) DefaultLauncher() string { return c.Defaults.Launcher }

// DefaultProvider returns the configured default provider name.
func (c Config) DefaultProvider() string { return c.Defaults.Provider }

// DefaultModel returns the configured default model name (may be empty).
func (c Config) DefaultModel() string { return c.Defaults.Model }

func (c *Config) normalize() {
	if c == nil {
		return
	}

	c.Defaults.Launcher = strings.ToLower(strings.TrimSpace(c.Defaults.Launcher))
	if c.Defaults.Launcher == "" {
		c.Defaults.Launcher = "tmux"
	}
	c.Defaults.Provider = strings.ToLower(strings.TrimSpace(c.Defaults.Provider))
	if c.Defaults.Provider == "" {
		c.Defaults.Provider = "claude"
	}
	c.Defaults.Model = strings.TrimSpace(c.Defaults.Model)

	c.Signals.CallbackHost = strings.TrimSpace(c.Signals.CallbackHost)
	if c.Signals.CallbackHost == "" {
		c.Signals.CallbackHost = defaultCallbackHost
	}
	if c.Signals.HandlerTimeout == 0 {
		c.Signals.HandlerTimeout = defaultHandlerTimeout
	}

	if c.Limits.MaxChildSessions == 0 {
		c.Limits.MaxChildSessions = defaultMaxChildSessions
	}
	if c.Limits.MaxSpawnDepth == 0 {
		c.Limits.MaxSpawnDepth = defaultMaxSpawnDepth
	}

	for i := range c.Signals.Escalate {
		c.Signals.Escalate[i].Type = strings.ToLower(strings.TrimSpace(c.Signals.Escalate[i].Type))
		if c.Signals.Escalate[i].Headers == nil {
			c.Signals.Escalate[i].Headers = map[string]string{}
		}
	}
	for i := range c.Signals.Spawn {
		c.Signals.Spawn[i].Type = strings.ToLower(strings.TrimSpace(c.Signals.Spawn[i].Type))
		if c.Signals.Spawn[i].Headers == nil {
			c.Signals.Spawn[i].Headers = map[string]string{}
		}
	}

	if c.Signals.Escalate == nil {
		c.Signals.Escalate = []SignalHandler{}
	}
	if c.Signals.Spawn == nil {
		c.Signals.Spawn = []SignalHandler{}
	}

	if c.Hooks.Timeout == 0 {
		c.Hooks.Timeout = 60 * time.Second
	}
}

func validateHandlers(path string, handlers []SignalHandler) error {
	for i, handler := range handlers {
		prefix := fmt.Sprintf("%s[%d]", path, i)
		handlerType := strings.ToLower(strings.TrimSpace(handler.Type))

		switch handlerType {
		case "stdout":
		case "webhook":
			if strings.TrimSpace(handler.URL) == "" {
				return fmt.Errorf("%s.url is required for webhook handlers", prefix)
			}
		case "exec":
			if len(handler.Argv) == 0 {
				return fmt.Errorf("%s.argv is required for exec handlers", prefix)
			}
		default:
			if handlerType == "" {
				return fmt.Errorf("%s.type is required", prefix)
			}
			return fmt.Errorf("%s.type %q is invalid (allowed: stdout, webhook, exec)", prefix, handler.Type)
		}
	}
	return nil
}
