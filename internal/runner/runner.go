// Package runner implements the core iteration loop orchestrator.
//
// The runner executes N iterations in foreground mode:
//
//  1. Initialize session state
//  2. For each iteration:
//     a. Build context.json (via internal/context)
//     b. Resolve prompt template (via internal/resolve)
//     c. Execute provider
//     d. Extract decision from provider stdout (via internal/extract)
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
	"github.com/hwells4/ap/internal/extract"
	"github.com/hwells4/ap/internal/judge"
	"github.com/hwells4/ap/internal/swarm"
	"github.com/hwells4/ap/internal/resolve"
	"github.com/hwells4/ap/internal/runtarget"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
	"github.com/hwells4/ap/internal/termination"
	"github.com/hwells4/ap/pkg/provider"
)

// extractToEscalate converts extract.EscalateSignal to signals.EscalateSignal.
func extractToEscalate(e *extract.EscalateSignal) *signals.EscalateSignal {
	if e == nil {
		return nil
	}
	return &signals.EscalateSignal{
		Type:    e.Type,
		Reason:  e.Reason,
		Options: e.Options,
	}
}

// extractToSpawnSignals parses the raw spawn JSON from extract.Signals
// into the typed []signals.SpawnSignal via the signals package.
func extractToSpawnSignals(raw json.RawMessage) []signals.SpawnSignal {
	if len(raw) == 0 {
		return nil
	}
	// Build a minimal agent_signals wrapper for the signals parser.
	wrapper := map[string]json.RawMessage{"spawn": raw}
	data, err := json.Marshal(wrapper)
	if err != nil {
		return nil
	}
	parsed, err := signals.Parse(data)
	if err != nil {
		return nil
	}
	return parsed.Spawn
}

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

	// OutputPath is the user-specified output file path from stage.yaml.
	// May contain ${SESSION} and ${ITERATION} placeholders.
	// Empty means no explicit output location (iteration-scoped output.md is used).
	OutputPath string

	// Model overrides the provider's default model. Empty uses the default.
	Model string

	// WorkDir is the working directory for provider execution.
	// Defaults to the current directory if empty.
	WorkDir string

	// RunTarget captures immutable project/repo/config spawn metadata.
	RunTarget runtarget.Target

	// Env contains additional environment variables for the provider.
	Env map[string]string

	// Store is the SQLite session store (required).
	Store *store.Store

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

	// CallbackURL is included in webhook payloads when an escalation callback
	// listener is active.
	CallbackURL string

	// CallbackToken is included in webhook payloads when callback auth is
	// enabled by the callback listener.
	CallbackToken string

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

	// Status is the final session state (e.g. store.StatusCompleted).
	Status string

	// Reason is the termination reason (if early stop).
	Reason string

	// Error is set when the run fails.
	Error string
}

// workManifest captures what an iteration actually produced.
type workManifest struct {
	Git *gitManifest `json:"git,omitempty"`
}

// gitManifest records git state changes across an iteration.
type gitManifest struct {
	PreHead      string   `json:"pre_head"`
	PostHead     string   `json:"post_head"`
	DiffStat     string   `json:"diff_stat"`
	FilesChanged []string `json:"files_changed"`
}

// buildWorkManifest captures git changes that occurred during an iteration.
// Returns an empty manifest (no git section) when there is no git repo or
// no changes were made.
func buildWorkManifest(workDir, preHead string) workManifest {
	if preHead == "" {
		return workManifest{}
	}
	postHead := gitHead(workDir)
	if postHead == "" || postHead == preHead {
		return workManifest{}
	}
	return workManifest{
		Git: &gitManifest{
			PreHead:      preHead,
			PostHead:     postHead,
			DiffStat:     gitDiffStat(workDir, preHead, postHead),
			FilesChanged: gitChangedFiles(workDir, preHead, postHead),
		},
	}
}

// Run executes the iteration loop for a single stage in foreground mode.
func Run(ctx context.Context, cfg Config) (runResult Result, runErr error) {
	// On crash/error, release in_progress beads for this session (best-effort).
	defer func() {
		if runErr != nil {
			_ = ReleaseBeads(cfg.Session, "")
		}
	}()

	if cfg.Store == nil {
		return Result{}, fmt.Errorf("runner: store is required")
	}
	normalizedCfg, err := normalizeRunTargetConfig(cfg)
	if err != nil {
		return Result{}, fmt.Errorf("runner: resolve run target: %w", err)
	}
	cfg = normalizedCfg

	if cfg.Pipeline != nil && len(cfg.Pipeline.Nodes) > 0 {
		return runPipeline(ctx, cfg)
	}

	// Persist run request for crash recovery and auditing.
	if err := persistRunRequest(cfg); err != nil {
		return Result{}, fmt.Errorf("runner: persist run request: %w", err)
	}

	// Create session in store.
	reqJSON := marshalRunRequestJSON(cfg)
	if err := cfg.Store.CreateSession(ctx, cfg.Session, "stage", cfg.StageName, reqJSON); err != nil {
		return Result{}, fmt.Errorf("runner: create session: %w", err)
	}
	_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
		"project_root":  cfg.RunTarget.ProjectRoot,
		"repo_root":     cfg.RunTarget.RepoRoot,
		"config_root":   cfg.RunTarget.ConfigRoot,
		"project_key":   cfg.RunTarget.ProjectKey,
		"target_source": cfg.RunTarget.Source,
	})
	if cfg.ParentSession != "" {
		_ = cfg.Store.AddChild(ctx, cfg.ParentSession, cfg.Session)
		_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
			"node_id": cfg.ParentSession,
		})
	}

	// Emit session_start event.
	startData := map[string]any{
		"stage":      cfg.StageName,
		"iterations": cfg.Iterations,
		"provider":   cfg.Provider.Name(),
		"run_target": runTargetPayload(cfg.RunTarget),
	}
	if cfg.ParentSession != "" {
		startData["parent_session"] = cfg.ParentSession
	}
	emitEvent(ctx, cfg, store.TypeSessionStart, "{}", startData)

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

	var lastResult extract.Result
	completed := 0
	spawnedChildren := 0
	injectedContext := "" // text from inject signal, consumed once
	var summaries []string

	for i := 1; i <= fixed.Target(); i++ {
		// Check context before starting iteration.
		if err := ctx.Err(); err != nil {
			emitEvent(ctx, cfg, store.TypeError, "{}", map[string]any{
				"error":      err.Error(),
				"type":       "signal",
				"iteration":  completed,
				"terminated": true,
			})
			storeMarkFailed(ctx, cfg, "signal_terminated", err.Error())
			emitEvent(ctx, cfg, store.TypeSessionComplete, "{}", map[string]any{
				"iterations":  completed,
				"reason":      "signal: " + err.Error(),
				"termination": "signal",
			})
			return Result{
				Iterations: completed,
				Status:     store.StatusFailed,
				Error:      err.Error(),
			}, nil
		}

		// Mark iteration started in store.
		iterStartTime := time.Now()
		_ = cfg.Store.StartIteration(ctx, store.IterationInput{
			SessionName:  cfg.Session,
			StageName:    cfg.StageName,
			Iteration:    i,
			ProviderName: cfg.Provider.Name(),
		})

		// Capture git HEAD before provider execution.
		preHead := ""
		if isGitRepo(cfg.WorkDir) {
			preHead = gitHead(cfg.WorkDir)
		}

		// Write session history for this iteration.
		historyPath := filepath.Join(cfg.RunDir, "history.md")
		writeHistory(ctx, cfg.Store, cfg.Session, historyPath)

		// Generate context.json.
		ctxPath, err := apcontext.GenerateContext(cfg.Session, i, stageConfig, cfg.RunDir, nil)
		if err != nil {
			return Result{}, fmt.Errorf("runner: generate context for iteration %d: %w", i, err)
		}

		// Resolve prompt template. Consume injected context if present.
		prompt := resolvePrompt(cfg.PromptTemplate, ctxPath, cfg.Session, i, stageConfig, injectedContext)
		injectedContext = "" // consumed

		// Read output path from context.json.
		ctxVars, _ := resolve.VarsFromContext(ctxPath)

		// Build provider request.
		req := provider.Request{
			Prompt:  prompt,
			Model:   cfg.Model,
			WorkDir: cfg.WorkDir,
			Env:     buildEnv(cfg, i),
		}

		// Execute provider with retry.
		maxAttempts := retryMaxAttempts(cfg)
		backoff := retryBackoff(cfg)
		var provResult provider.Result
		var provErr error
		var iterResult extract.Result

		cursorJSON := marshalCursorJSON(i, cfg.Provider.Name(), "", 0)

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			provResult, provErr = cfg.Provider.Execute(ctx, req)
			if provErr == nil {
				// Provider succeeded — extract decision from stdout.
				iterResult, _, _ = extract.Extract(provResult.Stdout, provResult.ExitCode)
				break
			}

			// On last attempt, don't retry.
			if attempt >= maxAttempts {
				break
			}

			// Emit retry event.
			emitEvent(ctx, cfg, store.TypeIterationRetried, cursorJSON, map[string]any{
				"iteration":    i,
				"attempt":      attempt,
				"max_attempts": maxAttempts,
				"error":        provErr.Error(),
				"backoff_ms":   backoff.Milliseconds(),
			})

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
			emitEvent(ctx, cfg, store.TypeIterationFailed, cursorJSON, map[string]any{
				"iteration": i,
				"error":     provErr.Error(),
				"exit_code": provResult.ExitCode,
				"attempts":  retryMaxAttempts(cfg),
			})
			completed = i

			// Check on_exhausted policy.
			if strings.ToLower(strings.TrimSpace(cfg.RetryOnExhausted)) == "pause" {
				escJSON, _ := json.Marshal(map[string]any{
					"type":   "retry_exhausted",
					"reason": fmt.Sprintf("retry exhausted after %d attempts: %s", retryMaxAttempts(cfg), provErr.Error()),
				})
				_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
					"status":          "paused",
					"escalation_json": string(escJSON),
				})
				return Result{
					Iterations: completed,
					Status:     store.StatusPaused,
					Reason:     "retry exhausted: " + provErr.Error(),
				}, nil
			}

			storeMarkFailed(ctx, cfg, "provider_error", provErr.Error())
			return Result{
				Iterations: completed,
				Status:     store.StatusFailed,
				Error:      provErr.Error(),
			}, nil
		}

		lastResult = iterResult

		// Build work manifest from git changes during this iteration.
		manifest := buildWorkManifest(cfg.WorkDir, preHead)
		manifestJSON, _ := json.Marshal(manifest)
		diffStat := ""
		if manifest.Git != nil {
			diffStat = manifest.Git.DiffStat
		}

		if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult, diffStat); err != nil {
			return Result{}, fmt.Errorf("runner: write iteration %d output: %w", i, err)
		}

		// Complete iteration in store (updates iteration_completed, emits event).
		signalsJSON, _ := json.Marshal(iterResult.Signals)
		_ = cfg.Store.CompleteIteration(ctx, store.IterationComplete{
			SessionName:  cfg.Session,
			StageName:    cfg.StageName,
			Iteration:    i,
			Decision:     iterResult.Decision,
			Summary:      iterResult.Summary,
			ExitCode:     provResult.ExitCode,
			SignalsJSON:  string(signalsJSON),
			Stdout:       provResult.Stdout,
			Stderr:       provResult.Stderr,
			ContextJSON:  string(manifestJSON),
			ProviderName: cfg.Provider.Name(),
			DurationMS:   time.Since(iterStartTime).Milliseconds(),
		})

		// Inject signal — store text for next iteration's ${CONTEXT}.
		if iterResult.Signals.Inject != "" {
			injectedContext = iterResult.Signals.Inject
			emitEvent(ctx, cfg, store.TypeSignalInject, cursorJSON, map[string]any{
				"iteration": i,
				"length":    len(injectedContext),
			})
		}

		// Collect summary for judgment history.
		summaries = append(summaries, iterResult.Summary)

		spawnRes, spawnErr := processSpawnSignals(cfg, i, extractToSpawnSignals(iterResult.Signals.Spawn), spawnedChildren)
		if spawnErr != nil {
			return Result{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
		}
		spawnedChildren = spawnRes.ChildCount

		// Record child sessions in store for lineage tracking.
		if len(spawnRes.ChildNames) > 0 {
			for _, child := range spawnRes.ChildNames {
				_ = cfg.Store.AddChild(ctx, cfg.Session, child)
			}
		}

		completed = i

		// Escalate signal — always pauses, overrides agent decision.
		if iterResult.Signals.Escalate != nil {
			esc := extractToEscalate(iterResult.Signals.Escalate)
			sigID := SignalID(i, "escalate", 0)

			// Two-phase: emit dispatching before the side effect.
			emitEvent(ctx, cfg, store.TypeSignalDispatching, cursorJSON, map[string]any{
				"signal_id":   sigID,
				"signal_type": "escalate",
				"iteration":   i,
			})

			escJSON, _ := json.Marshal(map[string]any{
				"type":    esc.Type,
				"reason":  esc.Reason,
				"options": esc.Options,
			})
			_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
				"status":          "paused",
				"escalation_json": string(escJSON),
			})

			emitEvent(ctx, cfg, store.TypeSignalEscalate, cursorJSON, map[string]any{
				"signal_id": sigID,
				"iteration": i,
				"type":      esc.Type,
				"reason":    esc.Reason,
				"options":   esc.Options,
			})
			if err := dispatchSignalHandlers(dispatchSignalInput{
				Store:         cfg.Store,
				Session:       cfg.Session,
				Stage:         cfg.StageName,
				Iteration:     i,
				SignalID:      sigID,
				SignalType:    "escalate",
				Handlers:      cfg.EscalateHandlers,
				Timeout:       cfg.SignalHandlerTimeout,
				Output:        cfg.SignalOutput,
				Escalation:    esc,
				CallbackURL:   cfg.CallbackURL,
				CallbackToken: cfg.CallbackToken,
			}); err != nil {
				return Result{}, fmt.Errorf("runner: dispatch escalate handlers: %w", err)
			}
			return Result{
				Iterations: completed,
				Status:     store.StatusPaused,
				Reason:     "escalation: " + esc.Reason,
			}, nil
		}

		// Evaluate judgment termination if configured.
		if judgment != nil && judgeEval != nil && !judgment.InFallback() {
			judgeStop, judgeReason := judgment.ShouldStop(ctx, i, judgeEval, summaries)

			// Emit judge verdict event.
			emitEvent(ctx, cfg, store.TypeJudgeVerdict, cursorJSON, map[string]any{
				"iteration":         i,
				"consecutive_stops": judgment.ConsecutiveStops(),
				"in_fallback":       judgment.InFallback(),
				"stop":              judgeStop,
			})

			// Emit fallback warning if triggered.
			if judgment.InFallback() {
				emitEvent(ctx, cfg, store.TypeJudgeFallback, cursorJSON, map[string]any{
					"iteration": i,
					"reason":    "judge failed 3 consecutive times, falling back to fixed-iteration termination",
				})
			}

			if judgeStop {
				finishSession(ctx, cfg, completed, judgeReason)
				return Result{
					Iterations: completed,
					Status:     store.StatusCompleted,
					Reason:     judgeReason,
				}, nil
			}
		}

		// Evaluate fixed termination.
		shouldStop, reason := fixed.ShouldStop(i, lastResult.Decision)
		if shouldStop {
			finishSession(ctx, cfg, completed, reason)
			return Result{
				Iterations: completed,
				Status:     store.StatusCompleted,
				Reason:     reason,
			}, nil
		}
	}

	// All iterations complete.
	reason := fmt.Sprintf("Completed %d iterations (max: %d)", completed, fixed.Target())
	finishSession(ctx, cfg, completed, reason)
	return Result{
		Iterations: completed,
		Status:     store.StatusCompleted,
		Reason:     reason,
	}, nil
}

type pipelineRunNode struct {
	ID         string
	StageName  string
	Iterations int
	Index      int
	Inputs     compile.Inputs
	Swarm      *compile.SwarmBlock // non-nil for swarm nodes
}

func runPipeline(ctx context.Context, cfg Config) (Result, error) {
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

	// Create pipeline session in store.
	reqJSON := marshalRunRequestJSON(cfg)
	if err := cfg.Store.CreateSession(ctx, cfg.Session, "pipeline", pipelineName, reqJSON); err != nil {
		return Result{}, fmt.Errorf("runner: create session: %w", err)
	}
	_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
		"project_root":  cfg.RunTarget.ProjectRoot,
		"repo_root":     cfg.RunTarget.RepoRoot,
		"config_root":   cfg.RunTarget.ConfigRoot,
		"project_key":   cfg.RunTarget.ProjectKey,
		"target_source": cfg.RunTarget.Source,
	})

	// Set initial pipeline stage info in store.
	type stageInfo struct {
		Name       string `json:"name"`
		Index      int    `json:"index"`
		Iterations int    `json:"iterations"`
		Type       string `json:"type,omitempty"`
	}
	stageInfos := make([]stageInfo, len(nodes))
	for i, node := range nodes {
		if node.Swarm != nil {
			stageInfos[i] = stageInfo{
				Name:  node.ID,
				Index: i,
				Type:  "swarm",
			}
		} else {
			stageInfos[i] = stageInfo{
				Name:       node.StageName,
				Index:      i,
				Iterations: node.Iterations,
			}
		}
	}
	stagesJSON, _ := json.Marshal(stageInfos)
	firstStageName := nodes[0].StageName
	if nodes[0].Swarm != nil {
		firstStageName = nodes[0].ID
	}
	_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
		"stages_json":   string(stagesJSON),
		"current_stage": firstStageName,
		"node_id":       nodes[0].ID,
	})

	emitEvent(ctx, cfg, store.TypeSessionStart, "{}", map[string]any{
		"stage":      nodes[0].StageName,
		"iterations": totalPlannedIterations(nodes),
		"provider":   cfg.Provider.Name(),
		"run_target": runTargetPayload(cfg.RunTarget),
		"pipeline": map[string]any{
			"name":  pipelineName,
			"nodes": len(nodes),
		},
	})

	completed := 0
	spawnedChildren := 0
	injectedContext := ""

	for nodeIdx, node := range nodes {
		// Handle swarm nodes.
		if node.Swarm != nil {
			_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
				"current_stage": node.ID,
				"node_id":       node.ID,
			})

			swarmResult, swarmErr := executeSwarmNode(ctx, cfg, node, nodeIdx, injectedContext)
			if swarmErr != nil {
				storeMarkFailed(ctx, cfg, "swarm_error", swarmErr.Error())
				return Result{
					Iterations: completed,
					Status:     store.StatusFailed,
					Error:      swarmErr.Error(),
				}, nil
			}

			completed += swarmResult.Iterations
			injectedContext = "" // consumed by swarm block

			// Append stage boundary marker.
			appendStageBoundary(cfg.RunDir, node.ID, swarmResult.Iterations)
			markStageCompleted(ctx, cfg, nodeIdx)
			continue
		}

		_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
			"current_stage": node.ID,
			"node_id":       node.ID,
		})

		nodeCfg := cfg
		nodeCfg.StageName = node.StageName
		nodeCfg.Iterations = node.Iterations

		// Resolve per-stage prompt template.
		def, stageErr := stage.ResolveStage(node.StageName, stage.ResolveOptions{
			ProjectRoot: cfg.WorkDir,
		})
		if stageErr != nil {
			storeMarkFailed(ctx, cfg, "stage_resolve_error", stageErr.Error())
			return Result{Status: store.StatusFailed, Iterations: completed, Error: stageErr.Error()}, nil
		}
		promptBytes, promptErr := def.ReadPrompt()
		if promptErr != nil {
			storeMarkFailed(ctx, cfg, "prompt_read_error", promptErr.Error())
			return Result{Status: store.StatusFailed, Iterations: completed, Error: promptErr.Error()}, nil
		}
		nodeCfg.PromptTemplate = string(promptBytes)

		stageConfig := buildPipelineStageConfig(node)
		stageConfig.OutputPath = def.ReadOutputPath()
		fixed := termination.NewFixed(termination.FixedConfig{Iterations: &node.Iterations})
		var lastResult extract.Result
		stageCompleted := 0

		for i := 1; i <= fixed.Target(); i++ {
			if err := ctx.Err(); err != nil {
				emitEvent(ctx, cfg, store.TypeError, "{}", map[string]any{
					"error":      err.Error(),
					"type":       "signal",
					"iteration":  completed,
					"terminated": true,
					"stage":      node.StageName,
					"node_id":    node.ID,
				})
				storeMarkFailed(ctx, cfg, "signal_terminated", err.Error())
				emitEvent(ctx, cfg, store.TypeSessionComplete, "{}", map[string]any{
					"iterations":       completed,
					"total_iterations": completed,
					"reason":           "signal: " + err.Error(),
					"termination":      "signal",
				})
				return Result{
					Iterations: completed,
					Status:     store.StatusFailed,
					Error:      err.Error(),
				}, nil
			}

			// Capture git HEAD before provider execution.
			nodePreHead := ""
			if isGitRepo(cfg.WorkDir) {
				nodePreHead = gitHead(cfg.WorkDir)
			}

			nodeIterStartTime := time.Now()
			if err := cfg.Store.StartIteration(ctx, store.IterationInput{
				SessionName:  cfg.Session,
				StageName:    node.ID,
				Iteration:    i,
				ProviderName: cfg.Provider.Name(),
			}); err != nil {
				emitEvent(ctx, cfg, store.TypeError, "{}", map[string]any{
					"error":     err.Error(),
					"type":      "store_tracking",
					"operation": "start_iteration",
					"iteration": i,
					"stage":     node.ID,
				})
			}

			cursorJSON := marshalCursorJSON(i, cfg.Provider.Name(), node.ID, nodeIdx+1)

			// Write session history for this iteration.
			historyPath := filepath.Join(cfg.RunDir, "history.md")
			writeHistory(ctx, cfg.Store, cfg.Session, historyPath)

			ctxPath, err := apcontext.GenerateContext(cfg.Session, i, stageConfig, cfg.RunDir, nil)
			if err != nil {
				return Result{}, fmt.Errorf("runner: generate context for iteration %d: %w", i, err)
			}

			prompt := resolvePrompt(nodeCfg.PromptTemplate, ctxPath, cfg.Session, i, stageConfig, injectedContext)
			injectedContext = ""

			ctxVars, _ := resolve.VarsFromContext(ctxPath)

			req := provider.Request{
				Prompt:  prompt,
				Model:   nodeCfg.Model,
				WorkDir: nodeCfg.WorkDir,
				Env:     buildEnv(nodeCfg, i),
			}

			provResult, provErr := nodeCfg.Provider.Execute(ctx, req)
			if provErr != nil {
				emitEvent(ctx, cfg, store.TypeIterationFailed, cursorJSON, map[string]any{
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"error":     provErr.Error(),
					"exit_code": provResult.ExitCode,
				})
				storeMarkFailed(ctx, cfg, "provider_error", provErr.Error())
				return Result{
					Iterations: completed,
					Status:     store.StatusFailed,
					Error:      provErr.Error(),
				}, nil
			}

			iterResult, _, _ := extract.Extract(provResult.Stdout, provResult.ExitCode)
			lastResult = iterResult

			// Build work manifest from git changes during this iteration.
			nodeManifest := buildWorkManifest(cfg.WorkDir, nodePreHead)
			nodeManifestJSON, _ := json.Marshal(nodeManifest)
			nodeDiffStat := ""
			if nodeManifest.Git != nil {
				nodeDiffStat = nodeManifest.Git.DiffStat
			}

			if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult, nodeDiffStat); err != nil {
				return Result{}, fmt.Errorf("runner: write stage output for %s iteration %d: %w", node.ID, i, err)
			}

			signalsJSON, _ := json.Marshal(iterResult.Signals)
			if err := cfg.Store.CompleteIteration(ctx, store.IterationComplete{
				SessionName:  cfg.Session,
				StageName:    node.ID,
				Iteration:    i,
				Decision:     iterResult.Decision,
				Summary:      iterResult.Summary,
				ExitCode:     provResult.ExitCode,
				SignalsJSON:  string(signalsJSON),
				Stdout:       provResult.Stdout,
				Stderr:       provResult.Stderr,
				ContextJSON:  string(nodeManifestJSON),
				ProviderName: cfg.Provider.Name(),
				DurationMS:   time.Since(nodeIterStartTime).Milliseconds(),
			}); err != nil {
				emitEvent(ctx, cfg, store.TypeError, cursorJSON, map[string]any{
					"error":     err.Error(),
					"type":      "store_tracking",
					"operation": "complete_iteration",
					"iteration": i,
					"stage":     node.ID,
				})
			}

			if iterResult.Signals.Inject != "" {
				injectedContext = iterResult.Signals.Inject
				emitEvent(ctx, cfg, store.TypeSignalInject, cursorJSON, map[string]any{
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"length":    len(injectedContext),
				})
			}

			spawnRes, spawnErr := processSpawnSignals(nodeCfg, i, extractToSpawnSignals(iterResult.Signals.Spawn), spawnedChildren)
			if spawnErr != nil {
				return Result{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
			}
			spawnedChildren = spawnRes.ChildCount

			if len(spawnRes.ChildNames) > 0 {
				for _, child := range spawnRes.ChildNames {
					_ = cfg.Store.AddChild(ctx, cfg.Session, child)
				}
			}

			completed++
			stageCompleted++

			if iterResult.Signals.Escalate != nil {
				esc := extractToEscalate(iterResult.Signals.Escalate)
				sigID := SignalID(i, "escalate", 0)

				emitEvent(ctx, cfg, store.TypeSignalDispatching, cursorJSON, map[string]any{
					"signal_id":   sigID,
					"signal_type": "escalate",
					"iteration":   i,
				})

				escJSON, _ := json.Marshal(map[string]any{
					"type":    esc.Type,
					"reason":  esc.Reason,
					"options": esc.Options,
				})
				_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
					"status":          "paused",
					"escalation_json": string(escJSON),
				})

				emitEvent(ctx, cfg, store.TypeSignalEscalate, cursorJSON, map[string]any{
					"signal_id": sigID,
					"iteration": i,
					"stage":     node.StageName,
					"node_id":   node.ID,
					"type":      esc.Type,
					"reason":    esc.Reason,
					"options":   esc.Options,
				})
				if err := dispatchSignalHandlers(dispatchSignalInput{
					Store:         cfg.Store,
					Session:       cfg.Session,
					Stage:         node.StageName,
					Iteration:     i,
					SignalID:      sigID,
					SignalType:    "escalate",
					Handlers:      cfg.EscalateHandlers,
					Timeout:       cfg.SignalHandlerTimeout,
					Output:        cfg.SignalOutput,
					Escalation:    esc,
					CallbackURL:   cfg.CallbackURL,
					CallbackToken: cfg.CallbackToken,
				}); err != nil {
					return Result{}, fmt.Errorf("runner: dispatch escalate handlers: %w", err)
				}
				return Result{
					Iterations: completed,
					Status:     store.StatusPaused,
					Reason:     "escalation: " + esc.Reason,
				}, nil
			}

			decision := strings.ToLower(strings.TrimSpace(lastResult.Decision))
			if decision == "error" {
				storeMarkFailed(ctx, cfg, "agent_error", fmt.Sprintf("agent requested error at stage %s iteration %d", node.StageName, i))
				return Result{
					Iterations: completed,
					Status:     store.StatusFailed,
					Error:      "agent requested error",
				}, nil
			}
			if decision == "stop" || i >= fixed.Target() {
				break
			}
		}

		// Append stage boundary marker to session-scoped progress.
		appendStageBoundary(cfg.RunDir, node.StageName, stageCompleted)

		// Mark stage completed in stages_json.
		markStageCompleted(ctx, cfg, nodeIdx)
	}

	reason := fmt.Sprintf("Completed %d iterations across %d stages", completed, len(nodes))
	finishSession(ctx, cfg, completed, reason)
	return Result{
		Iterations: completed,
		Status:     store.StatusCompleted,
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
		nodeID := strings.TrimSpace(node.ID)
		if node.Swarm != nil {
			// Swarm node: stage name is the node ID (no single stage).
			if nodeID == "" {
				nodeID = fmt.Sprintf("swarm-%d", idx)
			}
			nodes = append(nodes, pipelineRunNode{
				ID:       nodeID,
				Index:    idx,
				Inputs:   node.Inputs,
				Swarm:    node.Swarm,
			})
			continue
		}
		stageName := strings.TrimSpace(node.Stage)
		if stageName == "" {
			return nil, fmt.Errorf("runner: node[%d] stage is required", idx)
		}
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
		if node.Swarm != nil {
			// Estimate: sum of (providers * stage runs) for swarm blocks.
			for _, s := range node.Swarm.Stages {
				runs := s.Runs
				if runs <= 0 {
					runs = 1
				}
				total += len(node.Swarm.Providers) * runs
			}
		} else {
			total += node.Iterations
		}
	}
	return total
}

// swarmNodeResult captures the outcome of a swarm block execution.
type swarmNodeResult struct {
	Iterations int
}

// executeSwarmNode runs a swarm block within a pipeline.
func executeSwarmNode(ctx context.Context, cfg Config, node pipelineRunNode, nodeIdx int, injectedContext string) (swarmNodeResult, error) {
	if node.Swarm == nil {
		return swarmNodeResult{}, fmt.Errorf("runner: node %q is not a swarm node", node.ID)
	}

	// Create the swarm executor.
	executor := &swarmExecutor{
		cfg:     cfg,
		blockID: node.ID,
	}

	// Emit swarm.started event.
	emitEvent(ctx, cfg, store.TypeSwarmStart, "{}", map[string]any{
		"block_id":   node.ID,
		"node_index": nodeIdx,
		"providers":  len(node.Swarm.Providers),
		"stages":     len(node.Swarm.Stages),
	})

	// Build swarm config.
	swarmCfg := swarm.Config{
		RunDir:     cfg.RunDir,
		BlockID:    node.ID,
		BlockIndex: nodeIdx,
		Providers:  node.Swarm.Providers,
		Stages:     node.Swarm.Stages,
		Executor:   executor,
	}

	// Run the swarm block.
	result, err := swarm.Run(ctx, swarmCfg)

	// Count total iterations across all providers.
	totalIterations := 0
	for _, prov := range result.Providers {
		for _, stageRes := range prov.Stages {
			totalIterations += stageRes.Iterations
		}
	}

	if err != nil {
		emitEvent(ctx, cfg, store.TypeSwarmComplete, "{}", map[string]any{
			"block_id":   node.ID,
			"node_index": nodeIdx,
			"status":     "failed",
			"iterations": totalIterations,
			"error":      err.Error(),
		})
		return swarmNodeResult{Iterations: totalIterations}, err
	}

	emitEvent(ctx, cfg, store.TypeSwarmComplete, "{}", map[string]any{
		"block_id":      node.ID,
		"node_index":    nodeIdx,
		"status":        "completed",
		"iterations":    totalIterations,
		"manifest_path": result.ManifestPath,
	})

	return swarmNodeResult{Iterations: totalIterations}, nil
}

func normalizeRunTargetConfig(cfg Config) (Config, error) {
	source := strings.TrimSpace(cfg.RunTarget.Source)
	if source == "" {
		if strings.TrimSpace(cfg.ParentSession) != "" {
			source = runtarget.SourceSpawnInherit
		} else {
			source = runtarget.SourceCLI
		}
	}

	var (
		target runtarget.Target
		err    error
	)
	if strings.TrimSpace(cfg.RunTarget.ProjectRoot) == "" {
		target, err = runtarget.Resolve(cfg.WorkDir, source)
	} else {
		target, err = runtarget.NormalizeWithDefaults(cfg.RunTarget, source)
	}
	if err != nil {
		return Config{}, err
	}

	cfg.RunTarget = target
	cfg.WorkDir = target.ProjectRoot
	return cfg, nil
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
		OutputPath:    cfg.OutputPath,
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

func writeIterationOutput(path string, iterResult extract.Result, provResult provider.Result, diffStat string) error {
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
	if diffStat != "" {
		content += "\n\n## Files Changed\n\n```\n" + diffStat + "\n```"
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
func finishSession(ctx context.Context, cfg Config, iterations int, reason string) {
	now := time.Now().UTC().Format(time.RFC3339)
	if err := cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
		"status":       "completed",
		"completed_at": now,
	}); err != nil {
		emitEvent(ctx, cfg, store.TypeError, "{}", map[string]any{
			"error":     err.Error(),
			"type":      "store_tracking",
			"operation": "finish_session",
		})
	}
	emitEvent(ctx, cfg, store.TypeSessionComplete, "{}", map[string]any{
		"iterations":       iterations,
		"total_iterations": iterations,
		"reason":           reason,
	})
}

// storeMarkFailed marks the session as failed in the store.
func storeMarkFailed(_ context.Context, cfg Config, errType, errMsg string) {
	// Use background context so cleanup succeeds even if the original
	// context was cancelled (e.g. SIGINT/SIGTERM).
	_ = cfg.Store.UpdateSession(context.Background(), cfg.Session, map[string]any{
		"status":     "failed",
		"error":      errMsg,
		"error_type": errType,
	})
}

// markStageCompleted updates the stages_json to mark a stage as completed.
func markStageCompleted(ctx context.Context, cfg Config, nodeIdx int) {
	row, err := cfg.Store.GetSession(ctx, cfg.Session)
	if err != nil {
		return
	}
	var stages []map[string]any
	if json.Unmarshal([]byte(row.StagesJSON), &stages) != nil || nodeIdx >= len(stages) {
		return
	}
	stages[nodeIdx]["completed_at"] = time.Now().UTC().Format(time.RFC3339)
	updated, err := json.Marshal(stages)
	if err != nil {
		return
	}
	_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
		"stages_json": string(updated),
	})
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
		"session":         cfg.Session,
		"stage":           cfg.StageName,
		"provider":        cfg.Provider.Name(),
		"model":           cfg.Model,
		"iterations":      cfg.Iterations,
		"work_dir":        cfg.WorkDir,
		"run_dir":         cfg.RunDir,
		"prompt_template": cfg.PromptTemplate,
		"project_root":    cfg.RunTarget.ProjectRoot,
		"repo_root":       cfg.RunTarget.RepoRoot,
		"config_root":     cfg.RunTarget.ConfigRoot,
		"project_key":     cfg.RunTarget.ProjectKey,
		"target_source":   cfg.RunTarget.Source,
		"run_target":      runTargetPayload(cfg.RunTarget),
	}
	if len(cfg.Env) > 0 {
		payload["env"] = cfg.Env
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
	if cfg.ParentSession != "" {
		payload["parent_session"] = cfg.ParentSession
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run request: %w", err)
	}
	return os.WriteFile(filepath.Join(cfg.RunDir, "run_request.json"), append(data, '\n'), 0o600)
}

// marshalRunRequestJSON returns a JSON string for the run request payload,
// suitable for storing in the SQLite store.
func marshalRunRequestJSON(cfg Config) string {
	payload := map[string]any{
		"session":         cfg.Session,
		"stage":           cfg.StageName,
		"provider":        cfg.Provider.Name(),
		"model":           cfg.Model,
		"iterations":      cfg.Iterations,
		"work_dir":        cfg.WorkDir,
		"run_dir":         cfg.RunDir,
		"prompt_template": cfg.PromptTemplate,
		"project_root":    cfg.RunTarget.ProjectRoot,
		"repo_root":       cfg.RunTarget.RepoRoot,
		"config_root":     cfg.RunTarget.ConfigRoot,
		"project_key":     cfg.RunTarget.ProjectKey,
		"target_source":   cfg.RunTarget.Source,
		"run_target":      runTargetPayload(cfg.RunTarget),
	}
	if len(cfg.Env) > 0 {
		payload["env"] = cfg.Env
	}
	if cfg.ParentSession != "" {
		payload["parent_session"] = cfg.ParentSession
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func runTargetPayload(target runtarget.Target) map[string]any {
	return map[string]any{
		"project_root": target.ProjectRoot,
		"repo_root":    target.RepoRoot,
		"config_root":  target.ConfigRoot,
		"project_key":  target.ProjectKey,
		"source":       target.Source,
	}
}

// emitEvent is a best-effort helper that appends an event to the store.
// Uses background context so events are recorded even if the caller's
// context was cancelled (e.g. signal termination cleanup).
func emitEvent(_ context.Context, cfg Config, eventType, cursorJSON string, data map[string]any) {
	if cfg.Store == nil {
		return
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = cfg.Store.AppendEvent(context.Background(), cfg.Session, eventType, cursorJSON, string(dataJSON))
}

// marshalCursorJSON builds a cursor JSON string for event metadata.
func marshalCursorJSON(iteration int, providerName, nodePath string, nodeRun int) string {
	cursor := map[string]any{
		"iteration": iteration,
		"provider":  providerName,
	}
	if nodePath != "" {
		cursor["node_path"] = nodePath
		cursor["node_run"] = nodeRun
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return "{}"
	}
	return string(data)
}
