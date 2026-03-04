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

	"github.com/hwells4/ap/internal/compile"
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

	// Pipeline is an optional multi-stage pipeline plan.
	// When set with nodes, it takes precedence over StageName/Iterations.
	Pipeline *compile.Pipeline

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

	// RetryMaxAttempts is the maximum number of attempts per iteration.
	// 1 means no retry (default). Values > 1 enable retry with backoff.
	RetryMaxAttempts int

	// RetryBackoff is the initial backoff duration between retries.
	// Each subsequent retry doubles the wait. Zero uses default (5s).
	RetryBackoff time.Duration

	// RetryOnExhausted controls behavior when all retries are exhausted.
	// "abort" (default) fails the session. "pause" pauses for investigation.
	RetryOnExhausted string
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
	if cfg.Pipeline != nil && len(cfg.Pipeline.Nodes) > 0 {
		return runPipeline(ctx, cfg)
	}

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

		// Execute provider with retry.
		maxAttempts := retryMaxAttempts(cfg)
		backoff := retryBackoff(cfg)
		var provResult provider.Result
		var provErr error
		var iterResult result.Result

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			provResult, provErr = cfg.Provider.Execute(ctx, req)
			if provErr == nil {
				// Provider succeeded — try to load result.
				var loadErr error
				iterResult, _, loadErr = result.Load(resultFilePath, statusFilePath)
				if loadErr == nil {
					break // success
				}
				provErr = fmt.Errorf("result load: %w", loadErr)
			}

			// On last attempt, don't retry.
			if attempt >= maxAttempts {
				break
			}

			// Emit retry event.
			_ = ew.Append(events.NewEvent(events.TypeIterationRetried, cfg.Session, cursor, map[string]any{
				"iteration":    i,
				"attempt":      attempt,
				"max_attempts": maxAttempts,
				"error":        provErr.Error(),
				"backoff_ms":   backoff.Milliseconds(),
			}))

			// Wait with exponential backoff, respecting context cancellation.
			select {
			case <-ctx.Done():
				provErr = ctx.Err()
				break
			case <-time.After(backoff):
			}
			if ctx.Err() != nil {
				provErr = ctx.Err()
				break
			}
			backoff *= 2
		}

		if provErr != nil {
			// All attempts exhausted or context canceled.
			_ = ew.Append(events.NewEvent(events.TypeIterationFailed, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"error":     provErr.Error(),
				"exit_code": provResult.ExitCode,
				"attempts":  retryMaxAttempts(cfg),
			}))
			completed = i

			// Check on_exhausted policy.
			if strings.ToLower(strings.TrimSpace(cfg.RetryOnExhausted)) == "pause" {
				if _, err := state.MarkPaused(statePath, &state.EscalationInfo{
					Type:   "retry_exhausted",
					Reason: fmt.Sprintf("retry exhausted after %d attempts: %s", retryMaxAttempts(cfg), provErr.Error()),
				}); err != nil {
					return Result{}, fmt.Errorf("runner: mark paused on retry exhaustion: %w", err)
				}
				return Result{
					Iterations: completed,
					Status:     state.StatePaused,
					Reason:     "retry exhausted: " + provErr.Error(),
				}, nil
			}

			markFailed(statePath, "provider_error", provErr.Error())
			return Result{
				Iterations: completed,
				Status:     state.StateFailed,
				Error:      provErr.Error(),
			}, nil
		}

		lastResult = iterResult

		if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult); err != nil {
			return Result{}, fmt.Errorf("runner: write iteration %d output: %w", i, err)
		}

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

type pipelineRunNode struct {
	ID         string
	StageName  string
	Iterations int
	Index      int
	Inputs     compile.Inputs
}

func runPipeline(ctx context.Context, cfg Config) (Result, error) {
	statePath := statePath(cfg.RunDir)
	eventsPath := eventsPath(cfg.RunDir)
	ew := events.NewWriter(eventsPath)

	if err := persistRunRequest(cfg); err != nil {
		return Result{}, fmt.Errorf("runner: persist run request: %w", err)
	}

	nodes, err := pipelineNodes(cfg.Pipeline)
	if err != nil {
		return Result{}, err
	}

	pipelineName := strings.TrimSpace(cfg.Pipeline.Name)
	if pipelineName == "" {
		pipelineName = "pipeline"
	}

	if _, err := state.Init(statePath, cfg.Session, "pipeline", pipelineName); err != nil {
		return Result{}, fmt.Errorf("runner: init state: %w", err)
	}

	stageStates := make([]state.StageState, len(nodes))
	for i, node := range nodes {
		stageStates[i] = state.StageState{
			Name:       node.StageName,
			Index:      i,
			Iterations: node.Iterations,
		}
	}
	if _, err := state.Update(statePath, func(s *state.SessionState) error {
		s.Pipeline = pipelineName
		s.Stages = stageStates
		s.CurrentStage = nodes[0].StageName
		s.NodeID = nodes[0].ID
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("runner: initialize pipeline state: %w", err)
	}

	if err := ew.Append(events.NewEvent(events.TypeSessionStart, cfg.Session, nil, map[string]any{
		"stage":      nodes[0].StageName,
		"iterations": totalPlannedIterations(nodes),
		"provider":   cfg.Provider.Name(),
		"pipeline": map[string]any{
			"name":  pipelineName,
			"nodes": len(nodes),
		},
	})); err != nil {
		return Result{}, fmt.Errorf("runner: emit session_start: %w", err)
	}

	completed := 0
	spawnedChildren := 0
	injectedContext := ""

	for nodeIdx, node := range nodes {
		if _, err := state.Update(statePath, func(s *state.SessionState) error {
			s.CurrentStage = node.StageName
			s.NodeID = node.ID
			s.Iteration = 0
			s.IterationCompleted = 0
			return nil
		}); err != nil {
			return Result{}, fmt.Errorf("runner: set current node: %w", err)
		}

		nodeCfg := cfg
		nodeCfg.StageName = node.StageName
		nodeCfg.Iterations = node.Iterations

		stageConfig := buildPipelineStageConfig(node)
		fixed := termination.NewFixed(termination.FixedConfig{Iterations: &node.Iterations})
		var lastResult result.Result

		for i := 1; i <= fixed.Target(); i++ {
			if err := ctx.Err(); err != nil {
				_ = ew.Append(events.NewEvent(events.TypeError, cfg.Session, nil, map[string]any{
					"error":      err.Error(),
					"type":       "signal",
					"iteration":  completed,
					"terminated": true,
					"stage":      node.StageName,
					"node_id":    node.ID,
				}))
				markFailed(statePath, "signal_terminated", err.Error())
				_ = ew.Append(events.NewEvent(events.TypeSessionComplete, cfg.Session, nil, map[string]any{
					"iterations":       completed,
					"total_iterations": completed,
					"reason":           "signal: " + err.Error(),
					"termination":      "signal",
				}))
				return Result{
					Iterations: completed,
					Status:     state.StateFailed,
					Error:      err.Error(),
				}, nil
			}

			if _, err := state.MarkIterationStarted(statePath, i); err != nil {
				return Result{}, fmt.Errorf("runner: mark iteration %d started: %w", i, err)
			}

			cursor := &events.Cursor{
				NodePath:  node.ID,
				NodeRun:   nodeIdx + 1,
				Iteration: i,
				Provider:  cfg.Provider.Name(),
			}
			if err := ew.Append(events.NewEvent(events.TypeIterationStart, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"stage":     node.StageName,
				"node_id":   node.ID,
			})); err != nil {
				return Result{}, fmt.Errorf("runner: emit iteration_start: %w", err)
			}

			ctxPath, err := apcontext.GenerateContext(cfg.Session, i, stageConfig, cfg.RunDir)
			if err != nil {
				return Result{}, fmt.Errorf("runner: generate context for iteration %d: %w", i, err)
			}

			prompt := resolvePrompt(nodeCfg.PromptTemplate, ctxPath, cfg.Session, i, stageConfig, injectedContext)
			injectedContext = ""

			ctxVars, _ := resolve.VarsFromContext(ctxPath)
			statusFilePath := ctxVars.STATUS
			resultFilePath := ctxVars.RESULT

			req := provider.Request{
				Prompt:     prompt,
				Model:      nodeCfg.Model,
				WorkDir:    nodeCfg.WorkDir,
				Env:        buildEnv(nodeCfg, i),
				StatusPath: statusFilePath,
				ResultPath: resultFilePath,
			}

			provResult, provErr := nodeCfg.Provider.Execute(ctx, req)
			if provErr != nil {
				_ = ew.Append(events.NewEvent(events.TypeIterationFailed, cfg.Session, cursor, map[string]any{
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"error":     provErr.Error(),
					"exit_code": provResult.ExitCode,
				}))
				markFailed(statePath, "provider_error", provErr.Error())
				return Result{
					Iterations: completed,
					Status:     state.StateFailed,
					Error:      provErr.Error(),
				}, nil
			}

			iterResult, _, loadErr := result.Load(resultFilePath, statusFilePath)
			if loadErr != nil {
				_ = ew.Append(events.NewEvent(events.TypeIterationFailed, cfg.Session, cursor, map[string]any{
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"error":     fmt.Sprintf("result load: %v", loadErr),
				}))
				markFailed(statePath, "missing_status", loadErr.Error())
				return Result{
					Iterations: completed,
					Status:     state.StateFailed,
					Error:      loadErr.Error(),
				}, nil
			}
			lastResult = iterResult

			if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult); err != nil {
				return Result{}, fmt.Errorf("runner: write stage output for %s iteration %d: %w", node.ID, i, err)
			}

			outputVars := map[string]any{
				"decision": iterResult.Decision,
				"summary":  iterResult.Summary,
				"node_id":  node.ID,
			}
			if _, err := state.UpdateIteration(statePath, i, outputVars, node.StageName); err != nil {
				return Result{}, fmt.Errorf("runner: update iteration %d state: %w", i, err)
			}
			if _, err := state.MarkIterationCompleted(statePath, i); err != nil {
				return Result{}, fmt.Errorf("runner: mark iteration %d completed: %w", i, err)
			}

			if err := ew.Append(events.NewEvent(events.TypeIterationComplete, cfg.Session, cursor, map[string]any{
				"iteration": i,
				"stage":     node.StageName,
				"node_id":   node.ID,
				"decision":  iterResult.Decision,
				"summary":   iterResult.Summary,
				"duration":  provResult.Duration.String(),
			})); err != nil {
				return Result{}, fmt.Errorf("runner: emit iteration_complete: %w", err)
			}

			if iterResult.AgentSignals.Inject != "" {
				injectedContext = iterResult.AgentSignals.Inject
				_ = ew.Append(events.NewEvent(events.TypeSignalInject, cfg.Session, cursor, map[string]any{
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"length":    len(injectedContext),
				}))
			}

			spawnRes, spawnErr := processSpawnSignals(nodeCfg, ew, i, iterResult.AgentSignals.Spawn, spawnedChildren)
			if spawnErr != nil {
				return Result{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
			}
			spawnedChildren = spawnRes.ChildCount

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

			completed++

			if iterResult.AgentSignals.Escalate != nil {
				esc := iterResult.AgentSignals.Escalate
				sigID := SignalID(i, "escalate", 0)

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
					"stage":     node.StageName,
					"node_id":   node.ID,
					"type":      esc.Type,
					"reason":    esc.Reason,
					"options":   esc.Options,
				})); err != nil {
					return Result{}, fmt.Errorf("runner: emit signal.escalate: %w", err)
				}
				if err := dispatchSignalHandlers(dispatchSignalInput{
					Writer:     ew,
					Session:    cfg.Session,
					Stage:      node.StageName,
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

			decision := strings.ToLower(strings.TrimSpace(lastResult.Decision))
			if decision == "error" {
				markFailed(statePath, "agent_error", fmt.Sprintf("agent requested error at stage %s iteration %d", node.StageName, i))
				return Result{
					Iterations: completed,
					Status:     state.StateFailed,
					Error:      "agent requested error",
				}, nil
			}
			if decision == "stop" || i >= fixed.Target() {
				break
			}
		}

		if _, err := state.Update(statePath, func(s *state.SessionState) error {
			if nodeIdx < len(s.Stages) {
				completedAt := time.Now().UTC().Format(time.RFC3339)
				s.Stages[nodeIdx].CompletedAt = &completedAt
			}
			return nil
		}); err != nil {
			return Result{}, fmt.Errorf("runner: mark stage complete: %w", err)
		}
	}

	reason := fmt.Sprintf("Completed %d iterations across %d stages", completed, len(nodes))
	finishSession(statePath, ew, cfg.Session, completed, reason)
	return Result{
		Iterations: completed,
		Status:     state.StateCompleted,
		Reason:     reason,
	}, nil
}

func pipelineNodes(pipeline *compile.Pipeline) ([]pipelineRunNode, error) {
	if pipeline == nil {
		return nil, fmt.Errorf("runner: pipeline is nil")
	}
	if len(pipeline.Nodes) == 0 {
		return nil, fmt.Errorf("runner: pipeline has no nodes")
	}

	nodes := make([]pipelineRunNode, 0, len(pipeline.Nodes))
	for idx, node := range pipeline.Nodes {
		if node.Parallel != nil {
			return nil, fmt.Errorf("runner: node %q parallel blocks are not supported in sequential runner", strings.TrimSpace(node.ID))
		}
		stageName := strings.TrimSpace(node.Stage)
		if stageName == "" {
			return nil, fmt.Errorf("runner: node[%d] stage is required", idx)
		}
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" {
			nodeID = stageName
		}
		iterations := node.Runs
		if iterations <= 0 {
			iterations = 1
		}
		nodes = append(nodes, pipelineRunNode{
			ID:         nodeID,
			StageName:  stageName,
			Iterations: iterations,
			Index:      idx,
			Inputs:     node.Inputs,
		})
	}
	return nodes, nil
}

func totalPlannedIterations(nodes []pipelineRunNode) int {
	total := 0
	for _, node := range nodes {
		total += node.Iterations
	}
	return total
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
		ID:            cfg.StageName,
		Name:          cfg.StageName,
		Index:         &idx,
		MaxIterations: &cfg.Iterations,
	}
}

func buildPipelineStageConfig(node pipelineRunNode) apcontext.StageConfig {
	index := node.Index
	maxIterations := node.Iterations
	cfg := apcontext.StageConfig{
		ID:            node.ID,
		Name:          node.StageName,
		Index:         &index,
		MaxIterations: &maxIterations,
	}
	if strings.TrimSpace(node.Inputs.From) != "" || strings.TrimSpace(node.Inputs.Select) != "" {
		from := strings.TrimSpace(node.Inputs.From)
		selectMode := strings.TrimSpace(node.Inputs.Select)
		cfg.Inputs = &apcontext.InputsConfig{
			From:   from,
			Select: selectMode,
		}
	}
	return cfg
}

func writeIterationOutput(path string, iterResult result.Result, provResult provider.Result) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	content := strings.TrimSpace(iterResult.Summary)
	if content == "" {
		content = strings.TrimSpace(provResult.Stdout)
	}
	if content == "" {
		content = strings.TrimSpace(provResult.Output)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	return os.WriteFile(path, []byte(content+"\n"), 0o644)
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
		"iterations":       iterations,
		"total_iterations": iterations,
		"reason":           reason,
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

const (
	defaultRetryMaxAttempts = 1               // no retry by default
	defaultRetryBackoff     = 5 * time.Second // 5s initial backoff
)

// retryMaxAttempts returns the effective max attempts (minimum 1).
func retryMaxAttempts(cfg Config) int {
	if cfg.RetryMaxAttempts > 1 {
		return cfg.RetryMaxAttempts
	}
	return defaultRetryMaxAttempts
}

// retryBackoff returns the effective initial backoff duration.
func retryBackoff(cfg Config) time.Duration {
	if cfg.RetryBackoff > 0 {
		return cfg.RetryBackoff
	}
	return defaultRetryBackoff
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
	if cfg.Pipeline != nil && len(cfg.Pipeline.Nodes) > 0 {
		name := strings.TrimSpace(cfg.Pipeline.Name)
		if name == "" {
			name = "pipeline"
		}
		payload["pipeline"] = map[string]any{
			"name":  name,
			"nodes": len(cfg.Pipeline.Nodes),
		}
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
