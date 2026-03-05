// Package mock provides a deterministic provider for testing.
//
// MockProvider implements pkg/provider.Provider with configurable canned
// responses per iteration. It supports success, error, timeout, and
// no-decision scenarios without requiring external CLI tools.
//
// Decisions are emitted as ```ap-result fenced blocks appended to stdout,
// matching the extraction strategy used by internal/extract.
package mock

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Decision is the agent decision (continue, stop, error, etc.).
	Decision string

	// Summary is the agent summary.
	Summary string

	// Reason is the agent reason.
	Reason string

	// Signals contains optional agent signals (inject, escalate, etc.).
	Signals *Signals

	// Duration overrides the execution duration. Zero uses a small default.
	Duration time.Duration

	// Err is an error returned from Execute (simulates provider failure).
	Err error

	// EmitDecision controls whether an ap-result block is emitted in stdout.
	// Defaults to true when Decision is set. Set to false explicitly to
	// simulate an agent that produces no decision block.
	EmitDecision *bool

	// Delay is how long Execute blocks before returning (simulates work).
	Delay time.Duration
}

// Signals holds optional signal fields for the ap-result block.
type Signals struct {
	Inject   string          `json:"inject,omitempty"`
	Escalate *EscalateSignal `json:"escalate,omitempty"`
	Spawn    json.RawMessage `json:"spawn,omitempty"`
}

// EscalateSignal represents an escalation request.
type EscalateSignal struct {
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Options []string `json:"options,omitempty"`
}

// shouldEmitDecision returns whether an ap-result block should be appended to stdout.
func (r Response) shouldEmitDecision() bool {
	if r.EmitDecision != nil {
		return *r.EmitDecision
	}
	return r.Decision != ""
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
// When a decision is configured, it appends an ap-result fenced block to stdout.
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

	// Build stdout: original stdout + optional ap-result block.
	stdout := resp.Stdout
	if resp.shouldEmitDecision() {
		block := buildApResultBlock(resp)
		if stdout != "" {
			stdout += "\n"
		}
		stdout += block
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	result := provider.Result{
		Output:     stdout,
		Stdout:     stdout,
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

// buildApResultBlock constructs a ```ap-result fenced block from the response.
func buildApResultBlock(resp Response) string {
	payload := map[string]any{
		"decision": resp.Decision,
		"summary":  resp.Summary,
	}
	if resp.Reason != "" {
		payload["reason"] = resp.Reason
	}
	if resp.Signals != nil {
		signals := map[string]any{}
		if resp.Signals.Inject != "" {
			signals["inject"] = resp.Signals.Inject
		}
		if resp.Signals.Escalate != nil {
			signals["escalate"] = resp.Signals.Escalate
		}
		if len(resp.Signals.Spawn) > 0 {
			signals["spawn"] = json.RawMessage(resp.Signals.Spawn)
		}
		if len(signals) > 0 {
			payload["signals"] = signals
		}
	}

	data, _ := json.Marshal(payload)
	return "```ap-result\n" + string(data) + "\n```"
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
	}
}

// FailureResponse returns a Response with a provider-level error (non-zero exit).
func FailureResponse(err error) Response {
	return Response{
		ExitCode: 1,
		Err:      err,
	}
}

// InjectResponse returns a Response with an inject signal.
func InjectResponse(decision, summary, injectText string) Response {
	return Response{
		Decision: decision,
		Summary:  summary,
		Signals:  &Signals{Inject: injectText},
	}
}

// EscalateResponse returns a Response with an escalate signal.
func EscalateResponse(decision, summary, escalateType, reason string, options []string) Response {
	return Response{
		Decision: decision,
		Summary:  summary,
		Signals: &Signals{
			Escalate: &EscalateSignal{
				Type:    escalateType,
				Reason:  reason,
				Options: options,
			},
		},
	}
}

// SpawnResponse returns a Response with spawn signals.
func SpawnResponse(decision, summary string, spawns ...SpawnDef) Response {
	data, _ := json.Marshal(spawns)
	return Response{
		Decision: decision,
		Summary:  summary,
		Signals:  &Signals{Spawn: json.RawMessage(data)},
	}
}

// SpawnDef defines a spawn signal for SpawnResponse.
type SpawnDef struct {
	Run     string `json:"run"`
	Session string `json:"session"`
	Context string `json:"context,omitempty"`
}

// NoDecisionResponse returns a Response where no ap-result block is emitted.
// This simulates an agent that produces no structured decision.
func NoDecisionResponse() Response {
	emit := false
	return Response{
		EmitDecision: &emit,
	}
}
