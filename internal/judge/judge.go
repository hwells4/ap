// Package judge invokes a lightweight model to evaluate whether an agent loop
// should continue or stop. It provides two-agent consensus for termination
// decisions by asking a separate model to review iteration history.
package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hwells4/ap/pkg/provider"
)

const defaultMaxRetries = 3

// Verdict is the structured judgment returned by the judge model.
type Verdict struct {
	// Decision is "continue" or "stop".
	Decision string `json:"decision"`

	// Confidence is a 0.0–1.0 score indicating certainty.
	Confidence float64 `json:"confidence"`

	// Rationale explains the judgment.
	Rationale string `json:"rationale"`
}

// Request describes what the judge should evaluate.
type Request struct {
	// Session is the session name.
	Session string

	// Stage is the stage name.
	Stage string

	// Iteration is the current iteration number.
	Iteration int

	// Summaries is the list of per-iteration summaries up to now.
	Summaries []string

	// LastResult is the agent's most recent decision ("continue", "stop", etc).
	LastResult string
}

// Config holds judge configuration.
type Config struct {
	// Provider executes the judge prompt.
	Provider provider.Provider

	// Model overrides the provider's default model. Empty uses default.
	Model string

	// MaxRetries is the number of attempts before giving up on malformed output.
	// Zero or negative uses the default (3).
	MaxRetries int
}

// Judge evaluates iteration history to produce continue/stop verdicts.
type Judge struct {
	provider   provider.Provider
	model      string
	maxRetries int
}

// New creates a Judge from config.
func New(cfg Config) *Judge {
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = defaultMaxRetries
	}
	return &Judge{
		provider:   cfg.Provider,
		model:      cfg.Model,
		maxRetries: retries,
	}
}

// Evaluate asks the judge model whether the loop should continue or stop.
// It retries on malformed output up to MaxRetries times.
func (j *Judge) Evaluate(ctx context.Context, req Request) (Verdict, error) {
	prompt := buildPrompt(req)

	var lastErr error
	for attempt := 0; attempt < j.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return Verdict{}, fmt.Errorf("judge: context canceled: %w", err)
		}

		result, err := j.provider.Execute(ctx, provider.Request{
			Prompt: prompt,
			Model:  j.model,
		})
		if err != nil {
			return Verdict{}, fmt.Errorf("judge: provider execute: %w", err)
		}

		verdict, parseErr := parseVerdict(result.Stdout)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}

		return verdict, nil
	}

	return Verdict{}, fmt.Errorf("judge: exhausted %d retries: %w", j.maxRetries, lastErr)
}

// parseVerdict extracts a Verdict from the judge model's stdout.
func parseVerdict(output string) (Verdict, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return Verdict{}, fmt.Errorf("judge: empty output")
	}

	// Try to extract JSON from output (may have surrounding text).
	jsonStr := extractJSON(output)
	if jsonStr == "" {
		return Verdict{}, fmt.Errorf("judge: no JSON found in output")
	}

	var v Verdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return Verdict{}, fmt.Errorf("judge: parse verdict: %w", err)
	}

	v.Decision = strings.ToLower(strings.TrimSpace(v.Decision))
	if v.Decision != "continue" && v.Decision != "stop" {
		return Verdict{}, fmt.Errorf("judge: invalid decision %q (want continue or stop)", v.Decision)
	}

	return v, nil
}

// extractJSON finds the first JSON object in the string.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// buildPrompt constructs the judge evaluation prompt with iteration history.
func buildPrompt(req Request) string {
	var b strings.Builder
	b.WriteString("You are a judgment agent evaluating whether an autonomous AI agent loop should continue or stop.\n\n")
	b.WriteString(fmt.Sprintf("Session: %s\n", req.Session))
	b.WriteString(fmt.Sprintf("Stage: %s\n", req.Stage))
	b.WriteString(fmt.Sprintf("Current iteration: %d\n", req.Iteration))
	b.WriteString(fmt.Sprintf("Agent's last decision: %s\n\n", req.LastResult))

	b.WriteString("Iteration history:\n")
	for i, summary := range req.Summaries {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, summary))
	}

	b.WriteString("\nRespond with a single JSON object:\n")
	b.WriteString(`{"decision": "continue" or "stop", "confidence": 0.0-1.0, "rationale": "explanation"}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- \"stop\" if the task appears complete, is looping without progress, or has hit diminishing returns\n")
	b.WriteString("- \"continue\" if meaningful progress is being made and the task is not yet complete\n")
	b.WriteString("- confidence should reflect how certain you are about the decision\n")

	return b.String()
}
