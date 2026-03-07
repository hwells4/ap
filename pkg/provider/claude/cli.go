package claude

import (
	"context"
	"encoding/json"
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
	// DefaultBinary is the CLI name used to invoke Claude.
	DefaultBinary = "claude"
	// DefaultModel is the default Claude model.
	DefaultModel = "opus"
)

// SupportedModels lists the models supported by the Claude provider.
var SupportedModels = []string{
	"opus", "opus-4", "opus-4.5", "claude-opus",
	"sonnet", "sonnet-4", "claude-sonnet",
	"haiku", "claude-haiku",
}

// CLI invokes the Claude CLI as a provider implementation.
type CLI struct {
	Binary          string
	Model           string
	SkipPermissions bool

	initialized bool
}

// Option configures the Claude CLI provider.
type Option func(*CLI)

// New returns a Claude CLI provider with defaults applied.
func New(options ...Option) *CLI {
	cli := &CLI{
		Binary:          DefaultBinary,
		Model:           DefaultModel,
		SkipPermissions: true,
	}
	for _, option := range options {
		option(cli)
	}
	return cli
}

// WithBinary overrides the claude CLI binary name.
func WithBinary(binary string) Option {
	return func(cli *CLI) {
		if binary != "" {
			cli.Binary = binary
		}
	}
}

// WithDefaultModel overrides the default Claude model.
func WithDefaultModel(model string) Option {
	return func(cli *CLI) {
		if model != "" {
			cli.Model = model
		}
	}
}

// WithSkipPermissions toggles the --dangerously-skip-permissions flag.
func WithSkipPermissions(skip bool) Option {
	return func(cli *CLI) {
		cli.SkipPermissions = skip
	}
}

// Name returns the canonical provider name.
func (c *CLI) Name() string {
	return "claude"
}

// DefaultModel returns the default model for Claude.
func (c *CLI) DefaultModel() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

// Init initializes the Claude CLI provider.
func (c *CLI) Init(ctx context.Context) error {
	// Validate binary exists
	binary := c.Binary
	if binary == "" {
		binary = DefaultBinary
	}
	if _, err := osexec.LookPath(binary); err != nil {
		return fmt.Errorf("claude binary not found: %w", err)
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
		if err := validate.Model(c.Model, SupportedModels); err != nil {
			return fmt.Errorf("invalid model: %w", err)
		}
	}
	return nil
}

// Capabilities returns the Claude provider's feature set.
func (c *CLI) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Flags:           provider.CapabilityTools | provider.CapabilityVision,
		SupportedModels: SupportedModels,
		MaxPromptSize:   validate.DefaultMaxPromptSize,
		MaxOutputSize:   1 * 1024 * 1024, // 1 MiB
	}
}

// Execute runs the Claude CLI with the supplied prompt and model.
func (c *CLI) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	// Validate request
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

	args := []string{"--model", model, "--print", "-", "--output-format", "stream-json", "--verbose"}
	if c.SkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

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

	rawStdout := ""
	stderr := ""
	exitCode := -1
	duration := finished.Sub(started)
	if execResult != nil {
		rawStdout = string(execResult.Stdout)
		stderr = string(execResult.Stderr)
		exitCode = execResult.ExitCode
		duration = execResult.Duration
	}

	// The stdout is NDJSON (stream-json format). Extract the text content
	// for decision extraction, and preserve the raw stream for monitoring.
	textOutput := extractTextFromStreamJSON(rawStdout)

	result := provider.Result{
		Output:     textOutput,
		Stdout:     textOutput,
		Stderr:     stderr,
		ExitCode:   exitCode,
		Model:      model,
		StartedAt:  started,
		FinishedAt: finished,
		Duration:   duration,
		StreamJSON: rawStdout,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// ResolveModel maps model aliases to canonical Claude model names.
// This is the public API for callers that need to normalize before validation.
func ResolveModel(model string) string {
	return normalizeModel(model)
}

func normalizeModel(model string) string {
	lowered := strings.ToLower(strings.TrimSpace(model))
	switch lowered {
	case "claude-opus", "opus-4", "opus-4.5":
		return "opus"
	case "claude-sonnet", "sonnet-4":
		return "sonnet"
	case "claude-haiku":
		return "haiku"
	default:
		return lowered
	}
}

// extractTextFromStreamJSON reconstructs plain text output from Claude's
// stream-json NDJSON format. Each line is a JSON event; text content lives
// inside "assistant" events under message.content[].text. If the input
// doesn't look like NDJSON, it's returned as-is (graceful fallback).
func extractTextFromStreamJSON(ndjson string) string {
	if ndjson == "" {
		return ""
	}
	// Quick check: if first non-empty line doesn't start with '{', this
	// isn't stream-json — return as-is for backward compatibility.
	for _, line := range strings.SplitN(ndjson, "\n", 2) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if trimmed[0] != '{' {
				return ndjson
			}
			break
		}
	}

	var texts []string
	for _, line := range strings.Split(ndjson, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		var eventType string
		if raw, ok := event["type"]; ok {
			_ = json.Unmarshal(raw, &eventType)
		}
		if eventType != "assistant" {
			continue
		}

		// Parse message.content[] for text blocks.
		msgRaw, ok := event["message"]
		if !ok {
			continue
		}
		var message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(msgRaw, &message); err != nil {
			continue
		}
		for _, block := range message.Content {
			if block.Type == "text" && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
	}

	if len(texts) == 0 {
		// No assistant text found — fall back to raw output so extraction
		// can still attempt to find ap-result blocks or use defaults.
		return ndjson
	}
	return strings.Join(texts, "")
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
