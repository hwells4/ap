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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/config"
	apcontext "github.com/hwells4/ap/internal/context"
	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/judge"
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

	// EscalateHandlers dispatch escalation side effects in configured order.
	EscalateHandlers []config.SignalHandler

	// SpawnHandlers dispatch spawn side effects in configured order.
	SpawnHandlers []config.SignalHandler

	// SignalHandlerTimeout bounds webhook/exec handler runtime.
	SignalHandlerTimeout time.Duration

	// SignalOutput receives stdout handler output. Defaults to os.Stdout.
	SignalOutput io.Writer

	// OnEscalate preserves one-off handler override metadata from run requests.
	OnEscalate string

	// JudgeProvider is the provider used for judgment termination.
	// When set, the runner uses consensus-based termination instead of fixed.
	JudgeProvider provider.Provider

	// JudgeModel overrides the judge provider's default model.
	JudgeModel string

	// JudgeConsensus is the number of consecutive stop verdicts required.
	// Zero uses the default (2).
	JudgeConsensus int

	// JudgeMinIterations is the minimum iteration count before judgment
	// can trigger a stop. Zero uses the default (3).
	JudgeMinIterations int

	// JudgeMaxRetries is the maximum retry count for malformed judge output.
	// Zero uses the default (3).
	JudgeMaxRetries int

	// ParentSession is the name of the parent session that spawned this one.
	// Empty for root sessions.
	ParentSession string
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
	initState, err := state.Init(statePath, cfg.Session, "stage", cfg.StageName)
	if err != nil {
		return Result{}, fmt.Errorf("runner: init state: %w", err)
	}

	// Record parent session lineage if this is a child session.
	if cfg.ParentSession != "" {
		initState.ParentSession = cfg.ParentSession
		if err := state.Write(statePath, initState); err != nil {
			return Result{}, fmt.Errorf("runner: write parent session: %w", err)
		}
	}

	// Emit session_start event.
	startData := map[string]any{
		"stage":      cfg.StageName,
		"iterations": cfg.Iterations,
		"provider":   cfg.Provider.Name(),
	}
	if cfg.ParentSession != "" {
		startData["parent_session"] = cfg.ParentSession
	}
	if err := ew.Append(events.NewEvent(events.TypeSessionStart, cfg.Session, nil, startData)); err != nil {
		return Result{}, fmt.Errorf("runner: emit session_start: %w", err)
	}

	fixed := termination.NewFixed(termination.FixedConfig{Iterations: &cfg.Iterations})
	stageConfig := buildStageConfig(cfg)

	// Initialize judgment strategy if a judge provider is configured.
	var judgment *termination.Judgment
	var judgeEval termination.Evaluator
	if cfg.JudgeProvider != nil {
		judgment = termination.NewJudgment(termination.JudgmentConfig{
			ConsensusRequired: cfg.JudgeConsensus,
			MinIterations:     cfg.JudgeMinIterations,
		})
		judgeEval = judge.New(judge.Config{
			Provider:   cfg.JudgeProvider,
			Model:      cfg.JudgeModel,
			MaxRetries: cfg.JudgeMaxRetries,
		})
	}

	var lastResult result.Result
	completed := 0
	spawnedChildren := 0
	injectedContext := "" // text from inject signal, consumed once
	var summaries []string

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

		// Resolve prompt template. Consume injected context if present.
		prompt := resolvePrompt(cfg.PromptTemplate, ctxPath, cfg.Session, i, stageConfig, injectedContext)
		injectedContext = "" // consumed

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

		// Inject signal — store text for next iteration's ${CONTEXT}.
		if iterResult.AgentSignals.Inject != "" {
			injectedContext = iterResult.AgentSignals.Inject
			_ = ew.Append(events.NewEvent(events.TypeSignalInject, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"length":    len(injectedContext),
			}))
		}

		// Collect summary for judgment history.
		summaries = append(summaries, iterResult.Summary)

		spawnRes, spawnErr := processSpawnSignals(cfg, ew, i, iterResult.AgentSignals.Spawn, spawnedChildren)
		if spawnErr != nil {
			return Result{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
		}
		spawnedChildren = spawnRes.ChildCount

		// Record child sessions in state for lineage tracking.
		if len(spawnRes.ChildNames) > 0 {
			if _, err := state.Update(statePath, func(s *state.SessionState) error {
				for _, child := range spawnRes.ChildNames {
					s.AddChildSession(child)
				}
				return nil
			}); err != nil {
				return Result{}, fmt.Errorf("runner: record child sessions: %w", err)
			}
		}

		completed = i

		// Escalate signal — always pauses, overrides agent decision.
		if iterResult.AgentSignals.Escalate != nil {
			esc := iterResult.AgentSignals.Escalate
			sigID := SignalID(i, "escalate", 0)

			// Two-phase: emit dispatching before the side effect.
			if err := emitDispatching(ew, cfg.Session, cursor, sigID, "escalate", i); err != nil {
				return Result{}, fmt.Errorf("runner: emit escalate dispatching: %w", err)
			}
			if _, err := state.MarkPaused(statePath, &state.EscalationInfo{
				Type:    esc.Type,
				Reason:  esc.Reason,
				Options: esc.Options,
			}); err != nil {
				return Result{}, fmt.Errorf("runner: mark paused on escalation: %w", err)
			}
			if err := ew.Append(events.NewEvent(events.TypeSignalEscalate, cfg.Session, cursor, map[string]any{
				"signal_id": sigID,
				"iteration": i,
				"type":      esc.Type,
				"reason":    esc.Reason,
				"options":   esc.Options,
			})); err != nil {
				return Result{}, fmt.Errorf("runner: emit signal.escalate: %w", err)
			}
			if err := dispatchSignalHandlers(dispatchSignalInput{
				Writer:     ew,
				Session:    cfg.Session,
				Stage:      cfg.StageName,
				Iteration:  i,
				SignalID:   sigID,
				SignalType: "escalate",
				Handlers:   cfg.EscalateHandlers,
				Timeout:    cfg.SignalHandlerTimeout,
				Output:     cfg.SignalOutput,
				Escalation: esc,
			}); err != nil {
				return Result{}, fmt.Errorf("runner: dispatch escalate handlers: %w", err)
			}
			return Result{
				Iterations: completed,
				Status:     state.StatePaused,
				Reason:     "escalation: " + esc.Reason,
			}, nil
		}

		// Evaluate judgment termination if configured.
		if judgment != nil && judgeEval != nil && !judgment.InFallback() {
			judgeStop, judgeReason := judgment.ShouldStop(ctx, i, lastResult, judgeEval, summaries)
			cursor := &events.Cursor{Iteration: i, Provider: cfg.Provider.Name()}

			// Emit judge verdict event.
			_ = ew.Append(events.NewEvent(events.TypeJudgeVerdict, cfg.Session, cursor, map[string]any{
				"iteration":         i,
				"consecutive_stops": judgment.ConsecutiveStops(),
				"in_fallback":       judgment.InFallback(),
				"stop":              judgeStop,
			}))

			// Emit fallback warning if triggered.
			if judgment.InFallback() {
				_ = ew.Append(events.NewEvent(events.TypeJudgeFallback, cfg.Session, cursor, map[string]any{
					"iteration": i,
					"reason":    "judge failed 3 consecutive times, falling back to fixed-iteration termination",
				}))
			}

			if judgeStop {
				finishSession(statePath, ew, cfg.Session, completed, judgeReason)
				return Result{
					Iterations: completed,
					Status:     state.StateCompleted,
					Reason:     judgeReason,
				}, nil
			}
		}

		// Evaluate fixed termination.
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
// injectedContext is text from a previous inject signal, set as ${CONTEXT}.
func resolvePrompt(template, ctxPath, session string, iteration int, sc apcontext.StageConfig, injectedContext string) string {
	vars, err := resolve.VarsFromContext(ctxPath)
	if err != nil {
		// Fallback: resolve with minimal vars.
		vars = resolve.Vars{
			SESSION:   session,
			ITERATION: strconv.Itoa(iteration),
		}
	}
	if injectedContext != "" {
		vars.CONTEXT = injectedContext
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
	if override := strings.TrimSpace(cfg.OnEscalate); override != "" {
		payload["on_escalate"] = override
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run request: %w", err)
	}
	return os.WriteFile(filepath.Join(cfg.RunDir, "run_request.json"), append(data, '\n'), 0o644)
}
