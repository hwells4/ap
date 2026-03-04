// Package codex implements the Codex CLI provider for ap.
// It executes the codex binary with stdin prompt, bypass/ephemeral flags,
// and output capture parity with the Claude provider.
package codex

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"sort"
	"strings"
	"time"

	internalexec "github.com/hwells4/ap/internal/exec"
	"github.com/hwells4/ap/internal/validate"
	"github.com/hwells4/ap/pkg/provider"
)

const (
	// DefaultBinary is the CLI name used to invoke Codex.
	DefaultBinary = "codex"
	// DefaultModel is the default Codex model.
	DefaultModel = "gpt-5.3-codex"
)

// SupportedModels lists models supported by the Codex provider.
var SupportedModels = []string{
	"gpt-5.3-codex", "gpt-5.2-codex", "gpt-5-codex",
	"gpt-5.1-codex-max", "gpt-5.1-codex-mini",
	"o3", "o3-mini",
}

// CLI invokes the Codex CLI as a provider implementation.
type CLI struct {
	Binary string
	Model  string
	Bypass bool

	initialized bool
}

// Option configures the Codex CLI provider.
type Option func(*CLI)

// New returns a Codex CLI provider with defaults applied.
func New(options ...Option) *CLI {
	cli := &CLI{
		Binary: DefaultBinary,
		Model:  DefaultModel,
		Bypass: true,
	}
	for _, option := range options {
		option(cli)
	}
	return cli
}

// WithBinary overrides the codex CLI binary name.
func WithBinary(binary string) Option {
	return func(cli *CLI) {
		if binary != "" {
			cli.Binary = binary
		}
	}
}

// WithDefaultModel overrides the default Codex model.
func WithDefaultModel(model string) Option {
	return func(cli *CLI) {
		if model != "" {
			cli.Model = model
		}
	}
}

// WithBypass toggles the --dangerously-bypass-approvals-and-sandbox flag.
func WithBypass(bypass bool) Option {
	return func(cli *CLI) {
		cli.Bypass = bypass
	}
}

// Name returns the canonical provider name.
func (c *CLI) Name() string {
	return "codex"
}

// DefaultModel returns the default model for Codex.
func (c *CLI) DefaultModel() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

// Init initializes the Codex CLI provider.
func (c *CLI) Init(ctx context.Context) error {
	binary := c.Binary
	if binary == "" {
		binary = DefaultBinary
	}
	if _, err := osexec.LookPath(binary); err != nil {
		return fmt.Errorf("codex binary not found: %w", err)
	}
	c.initialized = true
	return nil
}

// Shutdown cleanly shuts down the provider.
func (c *CLI) Shutdown(ctx context.Context) error {
	c.initialized = false
	return nil
}

// Validate checks if the provider is properly configured.
func (c *CLI) Validate() error {
	binary := c.Binary
	if binary == "" {
		binary = DefaultBinary
	}
	if err := validate.Binary(binary); err != nil {
		return fmt.Errorf("invalid binary: %w", err)
	}
	if c.Model != "" {
		// Validate base model (strip reasoning suffix).
		base := BaseModel(c.Model)
		if err := validate.Model(base, SupportedModels); err != nil {
			return fmt.Errorf("invalid model: %w", err)
		}
	}
	return nil
}

// Capabilities returns the Codex provider's feature set.
func (c *CLI) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Flags:           provider.CapabilityTools,
		SupportedModels: SupportedModels,
		MaxPromptSize:   validate.DefaultMaxPromptSize,
		MaxOutputSize:   1 * 1024 * 1024, // 1 MiB
	}
}

// Execute runs the Codex CLI with the supplied prompt and model.
func (c *CLI) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if err := validate.Prompt(req.Prompt, 0); err != nil {
		return provider.Result{}, fmt.Errorf("invalid prompt: %w", err)
	}
	if err := validate.Env(req.Env); err != nil {
		return provider.Result{}, fmt.Errorf("invalid env: %w", err)
	}
	if req.WorkDir != "" {
		if _, err := validate.WorkDir(req.WorkDir); err != nil {
			return provider.Result{}, fmt.Errorf("invalid workdir: %w", err)
		}
	}

	model := req.Model
	if model == "" {
		model = c.DefaultModel()
	}
	model = normalizeModel(model)

	binary := c.Binary
	if binary == "" {
		binary = DefaultBinary
	}

	// Build args: --model MODEL --dangerously-bypass-approvals-and-sandbox --ephemeral -
	args := []string{"--model", model}
	if c.Bypass {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	args = append(args, "--ephemeral", "-")

	cmd := internalexec.Command(ctx, binary, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), formatEnv(req.Env)...)
	}
	cmd.Stdin = strings.NewReader(req.Prompt)

	started := time.Now()
	execResult, err := internalexec.Run(ctx, cmd, internalexec.DefaultOptions())
	finished := time.Now()

	stdout := ""
	stderr := ""
	exitCode := -1
	duration := finished.Sub(started)
	if execResult != nil {
		stdout = string(execResult.Stdout)
		stderr = string(execResult.Stderr)
		exitCode = execResult.ExitCode
		duration = execResult.Duration
	}

	result := provider.Result{
		Output:     stdout,
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exitCode,
		Model:      model,
		StartedAt:  started,
		FinishedAt: finished,
		Duration:   duration,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// ResolveModel normalizes Codex model names and handles reasoning syntax.
// Input "gpt-5.2-codex:xhigh" returns "gpt-5.2-codex:xhigh" (lowercased).
// The base model (before ":") is used for validation, the full string is
// passed to the CLI.
func ResolveModel(model string) string {
	return normalizeModel(model)
}

// BaseModel extracts the base model name, stripping any :reasoning suffix.
// "gpt-5.2-codex:xhigh" → "gpt-5.2-codex"
func BaseModel(model string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(model)), ":")
	return base
}

// normalizeModel lowercases model names for consistency.
// Preserves reasoning suffix if present (e.g., "gpt-5.2-codex:xhigh").
func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func formatEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	formatted := make([]string, 0, len(keys))
	for _, key := range keys {
		formatted = append(formatted, fmt.Sprintf("%s=%s", key, env[key]))
	}
	return formatted
}
