// Package runner implements the core iteration loop orchestrator.
//
// The runner executes N iterations in foreground mode:
//
//  1. Initialize session state
//  2. For each iteration:
//     a. Build context.json (via internal/context)
//     b. Resolve prompt template (via internal/resolve)
//     c. Execute provider
//     d. Parse status.json → normalized result
//     e. Evaluate termination
//     f. Update state
//     g. Emit events
//  3. Write final session state
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	apcontext "github.com/hwells4/ap/internal/context"
	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/resolve"
	"github.com/hwells4/ap/internal/result"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/state"
	"github.com/hwells4/ap/internal/termination"
	"github.com/hwells4/ap/pkg/provider"
)

// Config holds the parameters for a single-stage foreground run.
type Config struct {
	// Session is the session name.
	Session string

	// RunDir is the session run directory (.ap/runs/<session>).
	RunDir string

	// StageName is the stage identifier.
	StageName string

	// Provider executes each iteration.
	Provider provider.Provider

	// Iterations is the fixed iteration count (>0).
	Iterations int

	// PromptTemplate is the prompt text with ${VAR} placeholders.
	PromptTemplate string

	// Model overrides the provider's default model. Empty uses the default.
	Model string

	// WorkDir is the working directory for provider execution.
	// Defaults to the current directory if empty.
	WorkDir string

	// Env contains additional environment variables for the provider.
	Env map[string]string

	// Launcher is used for spawn signal child-session starts.
	Launcher session.Launcher

	// ExecutablePath overrides the binary used to launch child sessions.
	ExecutablePath string

	// SpawnMaxChildren limits successful child spawns per parent session.
	// Zero or negative uses the default (10).
	SpawnMaxChildren int

	// SpawnMaxDepth limits parent->child nesting depth. Zero or negative uses
	// the default (3).
	SpawnMaxDepth int

	// SpawnDepth is the current nesting depth for this session (root = 0).
	SpawnDepth int
}

// Result captures the outcome of a run.
type Result struct {
	// Iterations is the number of iterations actually completed.
	Iterations int

	// Status is the final session state.
	Status state.State

	// Reason is the termination reason (if early stop).
	Reason string

	// Error is set when the run fails.
	Error string
}

// Run executes the iteration loop for a single stage in foreground mode.
func Run(ctx context.Context, cfg Config) (Result, error) {
	statePath := statePath(cfg.RunDir)
	eventsPath := eventsPath(cfg.RunDir)
	ew := events.NewWriter(eventsPath)

	// Persist run request for crash recovery and auditing.
	if err := persistRunRequest(cfg); err != nil {
		return Result{}, fmt.Errorf("runner: persist run request: %w", err)
	}

	// Initialize session state.
	if _, err := state.Init(statePath, cfg.Session, "stage", cfg.StageName); err != nil {
		return Result{}, fmt.Errorf("runner: init state: %w", err)
	}

	// Emit session_start event.
	if err := ew.Append(events.NewEvent(events.TypeSessionStart, cfg.Session, nil, map[string]any{
		"stage":      cfg.StageName,
		"iterations": cfg.Iterations,
		"provider":   cfg.Provider.Name(),
	})); err != nil {
		return Result{}, fmt.Errorf("runner: emit session_start: %w", err)
	}

	fixed := termination.NewFixed(termination.FixedConfig{Iterations: &cfg.Iterations})
	stageConfig := buildStageConfig(cfg)

	var lastResult result.Result
	completed := 0
	spawnedChildren := 0

	for i := 1; i <= fixed.Target(); i++ {
		// Check context before starting iteration.
		if err := ctx.Err(); err != nil {
			_ = ew.Append(events.NewEvent(events.TypeError, cfg.Session, nil, map[string]any{
				"error":      err.Error(),
				"type":       "signal",
				"iteration":  completed,
				"terminated": true,
			}))
			markFailed(statePath, "signal_terminated", err.Error())
			_ = ew.Append(events.NewEvent(events.TypeSessionComplete, cfg.Session, nil, map[string]any{
				"iterations":  completed,
				"reason":      "signal: " + err.Error(),
				"termination": "signal",
			}))
			return Result{
				Iterations: completed,
				Status:     state.StateFailed,
				Error:      err.Error(),
			}, nil
		}

		// Mark iteration started in state.
		if _, err := state.MarkIterationStarted(statePath, i); err != nil {
			return Result{}, fmt.Errorf("runner: mark iteration %d started: %w", i, err)
		}

		// Emit iteration_start event.
		cursor := &events.Cursor{Iteration: i, Provider: cfg.Provider.Name()}
		if err := ew.Append(events.NewEvent(events.TypeIterationStart, cfg.Session, cursor, map[string]any{
			"iteration": i,
		})); err != nil {
			return Result{}, fmt.Errorf("runner: emit iteration_start: %w", err)
		}

		// Generate context.json.
		ctxPath, err := apcontext.GenerateContext(cfg.Session, i, stageConfig, cfg.RunDir)
		if err != nil {
			return Result{}, fmt.Errorf("runner: generate context for iteration %d: %w", i, err)
		}

		// Resolve prompt template.
		prompt := resolvePrompt(cfg.PromptTemplate, ctxPath, cfg.Session, i, stageConfig)

		// Read status path from context.json for provider request.
		ctxVars, _ := resolve.VarsFromContext(ctxPath)
		statusFilePath := ctxVars.STATUS
		resultFilePath := ctxVars.RESULT

		// Build provider request.
		req := provider.Request{
			Prompt:     prompt,
			Model:      cfg.Model,
			WorkDir:    cfg.WorkDir,
			Env:        buildEnv(cfg, i),
			StatusPath: statusFilePath,
			ResultPath: resultFilePath,
		}

		// Execute provider.
		provResult, provErr := cfg.Provider.Execute(ctx, req)
		if provErr != nil {
			// Provider failure: emit iteration.failed and mark session failed.
			_ = ew.Append(events.NewEvent(events.TypeIterationFailed, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"error":     provErr.Error(),
				"exit_code": provResult.ExitCode,
			}))
			completed = i
			markFailed(statePath, "provider_error", provErr.Error())
			return Result{
				Iterations: completed,
				Status:     state.StateFailed,
				Error:      provErr.Error(),
			}, nil
		}

		// Parse result from status.json / result.json.
		iterResult, _, loadErr := result.Load(resultFilePath, statusFilePath)
		if loadErr != nil {
			// Missing or invalid status.json: emit iteration.failed and fail.
			_ = ew.Append(events.NewEvent(events.TypeIterationFailed, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"error":     fmt.Sprintf("result load: %v", loadErr),
			}))
			completed = i
			markFailed(statePath, "missing_status", loadErr.Error())
			return Result{
				Iterations: completed,
				Status:     state.StateFailed,
				Error:      loadErr.Error(),
			}, nil
		}

		lastResult = iterResult

		// Update state with iteration output.
		outputVars := map[string]any{
			"decision": iterResult.Decision,
			"summary":  iterResult.Summary,
		}
		if _, err := state.UpdateIteration(statePath, i, outputVars, cfg.StageName); err != nil {
			return Result{}, fmt.Errorf("runner: update iteration %d state: %w", i, err)
		}

		// Mark iteration completed in state.
		if _, err := state.MarkIterationCompleted(statePath, i); err != nil {
			return Result{}, fmt.Errorf("runner: mark iteration %d completed: %w", i, err)
		}

		// Emit iteration_complete event.
		if err := ew.Append(events.NewEvent(events.TypeIterationComplete, cfg.Session, cursor, map[string]any{
			"iteration": i,
			"decision":  iterResult.Decision,
			"summary":   iterResult.Summary,
			"duration":  provResult.Duration.String(),
		})); err != nil {
			return Result{}, fmt.Errorf("runner: emit iteration_complete: %w", err)
		}

		var spawnErr error
		spawnedChildren, spawnErr = processSpawnSignals(cfg, ew, i, iterResult.AgentSignals.Spawn, spawnedChildren)
		if spawnErr != nil {
			return Result{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
		}

		completed = i

		// Escalate signal — always pauses, overrides agent decision.
		if iterResult.AgentSignals.Escalate != nil {
			esc := iterResult.AgentSignals.Escalate
			_ = ew.Append(events.NewEvent(events.TypeSignalEscalate, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"type":      esc.Type,
				"reason":    esc.Reason,
				"options":   esc.Options,
			}))
			_, _ = state.MarkPaused(statePath, &state.EscalationInfo{
				Type:    esc.Type,
				Reason:  esc.Reason,
				Options: esc.Options,
			})
			return Result{
				Iterations: completed,
				Status:     state.StatePaused,
				Reason:     "escalation: " + esc.Reason,
			}, nil
		}

		// Evaluate termination.
		shouldStop, reason := fixed.ShouldStop(i, lastResult)
		if shouldStop {
			finishSession(statePath, ew, cfg.Session, completed, reason)
			return Result{
				Iterations: completed,
				Status:     state.StateCompleted,
				Reason:     reason,
			}, nil
		}
	}

	// All iterations complete.
	reason := fmt.Sprintf("Completed %d iterations (max: %d)", completed, fixed.Target())
	finishSession(statePath, ew, cfg.Session, completed, reason)
	return Result{
		Iterations: completed,
		Status:     state.StateCompleted,
		Reason:     reason,
	}, nil
}

// resolvePrompt substitutes template variables into the prompt.
func resolvePrompt(template, ctxPath, session string, iteration int, sc apcontext.StageConfig) string {
	vars, err := resolve.VarsFromContext(ctxPath)
	if err != nil {
		// Fallback: resolve with minimal vars.
		vars = resolve.Vars{
			SESSION:   session,
			ITERATION: strconv.Itoa(iteration),
		}
	}
	return resolve.ResolveTemplate(template, vars)
}

// buildStageConfig builds a minimal StageConfig for context generation.
func buildStageConfig(cfg Config) apcontext.StageConfig {
	idx := 0
	return apcontext.StageConfig{
		Name:          cfg.StageName,
		Index:         &idx,
		MaxIterations: &cfg.Iterations,
	}
}

// buildEnv constructs the provider environment from config and iteration.
func buildEnv(cfg Config, iteration int) map[string]string {
	env := make(map[string]string, len(cfg.Env)+4)
	for k, v := range cfg.Env {
		env[k] = v
	}
	env["AP_AGENT"] = "1"
	env["AP_SESSION"] = cfg.Session
	env["AP_STAGE"] = cfg.StageName
	env["AP_ITERATION"] = strconv.Itoa(iteration)
	return env
}

// finishSession marks the session completed and emits session_complete.
func finishSession(statePath string, ew *events.Writer, session string, iterations int, reason string) {
	_, _ = state.MarkCompleted(statePath)
	_ = ew.Append(events.NewEvent(events.TypeSessionComplete, session, nil, map[string]any{
		"iterations": iterations,
		"reason":     reason,
	}))
}

// markFailed marks the session as failed in state.json.
func markFailed(statePath, errType, errMsg string) {
	_, _ = state.MarkFailed(statePath, errType, errMsg)
}

// statePath returns the state.json path for a run directory.
func statePath(runDir string) string {
	return runDir + "/state.json"
}

// eventsPath returns the events.jsonl path for a run directory.
func eventsPath(runDir string) string {
	return runDir + "/events.jsonl"
}

// persistRunRequest writes run_request.json to the run directory.
func persistRunRequest(cfg Config) error {
	if err := os.MkdirAll(cfg.RunDir, 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	payload := map[string]any{
		"session":    cfg.Session,
		"stage":      cfg.StageName,
		"provider":   cfg.Provider.Name(),
		"model":      cfg.Model,
		"iterations": cfg.Iterations,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run request: %w", err)
	}
	return os.WriteFile(filepath.Join(cfg.RunDir, "run_request.json"), append(data, '\n'), 0o644)
}
