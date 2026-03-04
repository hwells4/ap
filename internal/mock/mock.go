// Package mock provides a deterministic provider for testing.
//
// MockProvider implements pkg/provider.Provider with configurable canned
// responses per iteration. It supports success, error, timeout, and
// malformed status scenarios without requiring external CLI tools.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hwells4/ap/pkg/provider"
)

// Response defines a single canned provider response.
type Response struct {
	// Stdout is the stdout content returned by Execute.
	Stdout string

	// Stderr is the stderr content returned by Execute.
	Stderr string

	// ExitCode is the process exit code.
	ExitCode int

	// Decision is the agent decision written to status.json.
	Decision string

	// Summary is the agent summary written to status.json.
	Summary string

	// Reason is the agent reason written to status.json.
	Reason string

	// FilesTouched is the list of files reported in status.json.
	FilesTouched []string

	// ItemsCompleted is the list of work items reported in status.json.
	ItemsCompleted []string

	// Errors is the list of errors reported in status.json.
	Errors []string

	// Duration overrides the execution duration. Zero uses a small default.
	Duration time.Duration

	// Err is an error returned from Execute (simulates provider failure).
	Err error

	// WriteStatus controls whether a status.json file is written.
	// Defaults to true when Decision is set.
	WriteStatus *bool

	// StatusJSON overrides the status.json content entirely.
	// When set, Decision/Summary/Reason/Work/Errors fields are ignored.
	StatusJSON string

	// Delay is how long Execute blocks before returning (simulates work).
	Delay time.Duration
}

// shouldWriteStatus returns whether a status.json should be written.
func (r Response) shouldWriteStatus() bool {
	if r.WriteStatus != nil {
		return *r.WriteStatus
	}
	return r.Decision != "" || r.StatusJSON != ""
}

// Provider is a deterministic mock implementation of pkg/provider.Provider.
type Provider struct {
	mu          sync.Mutex
	name        string
	model       string
	responses   []Response
	fallback    *Response
	calls       []Call
	initialized bool
}

// Call records a single Execute invocation for inspection.
type Call struct {
	Request   provider.Request
	Iteration int
	Time      time.Time
}

// Option configures the mock Provider.
type Option func(*Provider)

// New creates a MockProvider with the given options.
func New(opts ...Option) *Provider {
	p := &Provider{
		name:  "mock",
		model: "mock-default",
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// WithName sets the provider name.
func WithName(name string) Option {
	return func(p *Provider) {
		if name != "" {
			p.name = name
		}
	}
}

// WithModel sets the default model name.
func WithModel(model string) Option {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}

// WithResponses sets the per-iteration responses (0-indexed).
// The i-th Execute call returns responses[i]. If i >= len(responses),
// the fallback response is used.
func WithResponses(responses ...Response) Option {
	return func(p *Provider) {
		p.responses = responses
	}
}

// WithFallback sets the response used when the call index exceeds
// the configured responses slice. If no fallback is set and no
// response exists for the current call, Execute returns "continue".
func WithFallback(r Response) Option {
	return func(p *Provider) {
		p.fallback = &r
	}
}

// Name returns the canonical provider name.
func (p *Provider) Name() string {
	return p.name
}

// DefaultModel returns the default model.
func (p *Provider) DefaultModel() string {
	return p.model
}

// Init marks the provider as initialized.
func (p *Provider) Init(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = true
	return nil
}

// Shutdown marks the provider as shut down.
func (p *Provider) Shutdown(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = false
	return nil
}

// Validate always returns nil for the mock provider.
func (p *Provider) Validate() error {
	return nil
}

// Capabilities returns a minimal capability set.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Flags:           provider.CapabilityNone,
		SupportedModels: []string{p.model},
		MaxPromptSize:   10 * 1024 * 1024, // 10 MiB
		MaxOutputSize:   1 * 1024 * 1024,  // 1 MiB
	}
}

// Execute returns the canned response for the current call index.
// It writes status.json to req.StatusPath when configured.
func (p *Provider) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	p.mu.Lock()
	callIdx := len(p.calls)
	p.calls = append(p.calls, Call{
		Request:   req,
		Iteration: callIdx + 1,
		Time:      time.Now(),
	})
	resp := p.responseFor(callIdx)
	p.mu.Unlock()

	// Simulate delay, respecting context cancellation.
	if resp.Delay > 0 {
		select {
		case <-time.After(resp.Delay):
		case <-ctx.Done():
			return provider.Result{}, ctx.Err()
		}
	}

	// Check context after delay.
	if err := ctx.Err(); err != nil {
		return provider.Result{}, err
	}

	started := time.Now()
	duration := resp.Duration
	if duration == 0 {
		duration = time.Millisecond
	}
	finished := started.Add(duration)

	// Write status.json if configured.
	if resp.shouldWriteStatus() && req.StatusPath != "" {
		if err := writeStatus(req.StatusPath, resp); err != nil {
			return provider.Result{}, fmt.Errorf("mock: write status: %w", err)
		}
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	result := provider.Result{
		Output:     resp.Stdout,
		Stdout:     resp.Stdout,
		Stderr:     resp.Stderr,
		ExitCode:   resp.ExitCode,
		Model:      model,
		StartedAt:  started,
		FinishedAt: finished,
		Duration:   duration,
	}

	if resp.Err != nil {
		return result, resp.Err
	}

	return result, nil
}

// Calls returns a copy of all recorded Execute calls.
func (p *Provider) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Call, len(p.calls))
	copy(out, p.calls)
	return out
}

// CallCount returns the number of Execute calls made.
func (p *Provider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// Reset clears recorded calls and reinitializes the provider.
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

// responseFor returns the response for the given call index.
func (p *Provider) responseFor(idx int) Response {
	if idx < len(p.responses) {
		return p.responses[idx]
	}
	if p.fallback != nil {
		return *p.fallback
	}
	// Default: continue with empty summary.
	return Response{
		Decision: "continue",
		Summary:  fmt.Sprintf("mock iteration %d", idx+1),
	}
}

// writeStatus writes a status.json file at the given path.
func writeStatus(path string, resp Response) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create status dir: %w", err)
	}

	var payload []byte
	if resp.StatusJSON != "" {
		payload = []byte(resp.StatusJSON)
	} else {
		filesTouched := resp.FilesTouched
		if filesTouched == nil {
			filesTouched = []string{}
		}
		itemsCompleted := resp.ItemsCompleted
		if itemsCompleted == nil {
			itemsCompleted = []string{}
		}
		errs := resp.Errors
		if errs == nil {
			errs = []string{}
		}

		status := map[string]any{
			"decision": resp.Decision,
			"reason":   resp.Reason,
			"summary":  resp.Summary,
			"work": map[string]any{
				"items_completed": itemsCompleted,
				"files_touched":   filesTouched,
			},
			"errors": errs,
		}

		var err error
		payload, err = json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal status: %w", err)
		}
	}

	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

// ContinueResponse returns a Response that signals "continue".
func ContinueResponse(summary string) Response {
	return Response{
		Decision: "continue",
		Summary:  summary,
	}
}

// StopResponse returns a Response that signals "stop".
func StopResponse(summary, reason string) Response {
	return Response{
		Decision: "stop",
		Summary:  summary,
		Reason:   reason,
	}
}

// ErrorResponse returns a Response that signals "error".
func ErrorResponse(summary, reason string, errs []string) Response {
	return Response{
		Decision: "error",
		Summary:  summary,
		Reason:   reason,
		Errors:   errs,
	}
}

// FailureResponse returns a Response with a provider-level error (non-zero exit).
func FailureResponse(err error) Response {
	return Response{
		ExitCode: 1,
		Err:      err,
	}
}

// NoStatusResponse returns a Response where no status.json is written.
func NoStatusResponse() Response {
	writeStatus := false
	return Response{
		WriteStatus: &writeStatus,
	}
}
