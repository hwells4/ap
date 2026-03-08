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
	"github.com/hwells4/ap/internal/resolve"
	"github.com/hwells4/ap/internal/runtarget"
	"github.com/hwells4/ap/internal/session"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
	"github.com/hwells4/ap/internal/swarm"
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

	// Hooks defines lifecycle hook commands for this run.
	Hooks LifecycleHooks

	// HookTimeout bounds lifecycle hook execution. Zero uses default (60s).
	HookTimeout time.Duration

	// IterationTimeout bounds a single provider execution. Zero means no
	// timeout (the provider runs until it finishes or the session context
	// is cancelled). When exceeded, the iteration is treated as a provider
	// failure and follows the retry/on_exhausted policy.
	IterationTimeout time.Duration

	// InjectedContext seeds the ${CONTEXT} variable for the first iteration.
	// Used by resume --context to pass override text from the user.
	InjectedContext string
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

type iterationRetryConfig struct {
	MaxAttempts           int
	Backoff               time.Duration
	OnExhausted           string
	CountExhaustedFailure int
}

type iterationJudgmentConfig struct {
	Strategy  *termination.Judgment
	Evaluator termination.Evaluator
	Summaries []string
}

type iterationTerminationConfig struct {
	Fixed            termination.Fixed
	Judgment         *iterationJudgmentConfig
	StopStatus       string
	FailOnAgentError bool
}

type iterationParams struct {
	cfg                      Config
	hookCtx                  *HookContext
	iteration                int
	providerFailureHookIter  int
	stageConfig              apcontext.StageConfig
	promptTemplate           string
	storeStageName           string
	hookStageName            string
	cursorJSON               string
	iterationEventData       map[string]any
	signalEventData          map[string]any
	outputErrorMessage       string
	injectedContext          string
	spawnedChildren          int
	retry                    iterationRetryConfig
	termination              iterationTerminationConfig
	contextOpts              *apcontext.GenerateContextOpts
	onStartIterationError    func(error)
	onCompleteIterationError func(error)
}

type iterationOutcome struct {
	completedDelta  int
	injectedContext string
	spawnedChildren int
	stop            bool
	status          string
	reason          string
	err             string
}

func mergeEventData(base map[string]any, extras map[string]any) map[string]any {
	if len(extras) == 0 {
		return base
	}
	merged := make(map[string]any, len(base)+len(extras))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extras {
		merged[k] = v
	}
	return merged
}

func executeIteration(ctx context.Context, params iterationParams) (iterationOutcome, error) {
	cfg := params.cfg
	i := params.iteration
	iterStartTime := time.Now()

	if err := cfg.Store.StartIteration(ctx, store.IterationInput{
		SessionName:  cfg.Session,
		StageName:    params.storeStageName,
		Iteration:    i,
		ProviderName: cfg.Provider.Name(),
	}); err != nil && params.onStartIterationError != nil {
		params.onStartIterationError(err)
	}

	params.hookCtx.SetStage(params.hookStageName)
	params.hookCtx.SetIteration(i)
	params.hookCtx.Fire(ctx, "pre_iteration")

	preHead := ""
	if isGitRepo(cfg.WorkDir) {
		preHead = gitHead(cfg.WorkDir)
	}

	ctxPath, err := apcontext.GenerateContext(cfg.Session, i, params.stageConfig, cfg.RunDir, params.contextOpts)
	if err != nil {
		return iterationOutcome{}, fmt.Errorf("runner: generate context for iteration %d: %w", i, err)
	}

	ctxVars, _ := resolve.VarsFromContext(ctxPath)

	// Write history.md to the iteration directory (path from context.json).
	if ctxVars.HISTORY != "" {
		writeHistory(ctx, cfg.Store, cfg.Session, ctxVars.HISTORY)
	}

	prompt := resolvePrompt(params.promptTemplate, ctxPath, cfg.Session, i, params.stageConfig, params.injectedContext)

	req := provider.Request{
		Prompt:  prompt,
		Model:   cfg.Model,
		WorkDir: cfg.WorkDir,
		Env:     buildEnv(cfg, i),
	}

	maxAttempts := params.retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	backoff := params.retry.Backoff
	if backoff <= 0 {
		backoff = defaultRetryBackoff
	}

	var provResult provider.Result
	var provErr error
	var iterResult extract.Result

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		provResult, provErr = executeWithTimeout(ctx, cfg.Provider, req, cfg.IterationTimeout)
		if provErr == nil {
			iterResult, _, _ = extract.Extract(provResult.Stdout, provResult.ExitCode)
			break
		}

		if attempt >= maxAttempts {
			break
		}

		emitEvent(ctx, cfg, store.TypeIterationRetried, params.cursorJSON, mergeEventData(map[string]any{
			"iteration":    i,
			"attempt":      attempt,
			"max_attempts": maxAttempts,
			"error":        provErr.Error(),
			"backoff_ms":   backoff.Milliseconds(),
		}, params.iterationEventData))

		select {
		case <-ctx.Done():
			provErr = ctx.Err()
		case <-time.After(backoff):
		}
		if ctx.Err() != nil {
			provErr = ctx.Err()
			break
		}
		backoff *= 2
	}

	if provErr != nil {
		emitEvent(ctx, cfg, store.TypeIterationFailed, params.cursorJSON, mergeEventData(map[string]any{
			"iteration": i,
			"error":     provErr.Error(),
			"exit_code": provResult.ExitCode,
			"attempts":  maxAttempts,
		}, params.iterationEventData))

		outcome := iterationOutcome{
			completedDelta: params.retry.CountExhaustedFailure,
			stop:           true,
		}

		if strings.EqualFold(strings.TrimSpace(params.retry.OnExhausted), "pause") {
			escJSON, _ := json.Marshal(map[string]any{
				"type":   "retry_exhausted",
				"reason": fmt.Sprintf("retry exhausted after %d attempts: %s", maxAttempts, provErr.Error()),
			})
			_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
				"status":          "paused",
				"escalation_json": string(escJSON),
			})
			outcome.status = store.StatusPaused
			outcome.reason = "retry exhausted: " + provErr.Error()
			return outcome, nil
		}

		storeMarkFailed(ctx, cfg, "provider_error", provErr.Error())
		params.hookCtx.SetStage(params.hookStageName)
		params.hookCtx.SetIteration(params.providerFailureHookIter)
		params.hookCtx.SetStatus("failed")
		params.hookCtx.Fire(ctx, "on_failure")

		outcome.status = store.StatusFailed
		outcome.err = provErr.Error()
		return outcome, nil
	}

	manifest := buildWorkManifest(cfg.WorkDir, preHead)
	manifestJSON, _ := json.Marshal(manifest)
	diffStat := ""
	if manifest.Git != nil {
		diffStat = manifest.Git.DiffStat
	}

	if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult, diffStat); err != nil {
		return iterationOutcome{}, fmt.Errorf("runner: %s: %w", params.outputErrorMessage, err)
	}

	signalsJSON, _ := json.Marshal(iterResult.Signals)
	if err := cfg.Store.CompleteIteration(ctx, store.IterationComplete{
		SessionName:  cfg.Session,
		StageName:    params.storeStageName,
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
		StreamJSON:   provResult.StreamJSON,
	}); err != nil && params.onCompleteIterationError != nil {
		params.onCompleteIterationError(err)
	}

	params.hookCtx.SetStage(params.hookStageName)
	params.hookCtx.SetIteration(i)
	params.hookCtx.SetSummary(iterResult.Summary)
	params.hookCtx.Fire(ctx, "post_iteration")

	injectedContext := ""
	if iterResult.Signals.Inject != "" {
		injectedContext = iterResult.Signals.Inject
		emitEvent(ctx, cfg, store.TypeSignalInject, params.cursorJSON, mergeEventData(map[string]any{
			"iteration": i,
			"length":    len(injectedContext),
		}, params.signalEventData))
	}

	if params.termination.Judgment != nil {
		params.termination.Judgment.Summaries = append(params.termination.Judgment.Summaries, iterResult.Summary)
	}

	spawnRes, spawnErr := processSpawnSignals(cfg, i, extractToSpawnSignals(iterResult.Signals.Spawn), params.spawnedChildren)
	if spawnErr != nil {
		return iterationOutcome{}, fmt.Errorf("runner: process spawn signals: %w", spawnErr)
	}
	if len(spawnRes.ChildNames) > 0 {
		for _, child := range spawnRes.ChildNames {
			_ = cfg.Store.AddChild(ctx, cfg.Session, child)
		}
	}

	outcome := iterationOutcome{
		completedDelta:  1,
		injectedContext: injectedContext,
		spawnedChildren: spawnRes.ChildCount,
	}

	if iterResult.Signals.Escalate != nil {
		esc := extractToEscalate(iterResult.Signals.Escalate)
		sigID := SignalID(i, "escalate", 0)

		emitEvent(ctx, cfg, store.TypeSignalDispatching, params.cursorJSON, mergeEventData(map[string]any{
			"signal_id":   sigID,
			"signal_type": "escalate",
			"iteration":   i,
		}, nil))

		escJSON, _ := json.Marshal(map[string]any{
			"type":    esc.Type,
			"reason":  esc.Reason,
			"options": esc.Options,
		})
		_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
			"status":          "paused",
			"escalation_json": string(escJSON),
		})

		emitEvent(ctx, cfg, store.TypeSignalEscalate, params.cursorJSON, mergeEventData(map[string]any{
			"signal_id": sigID,
			"iteration": i,
			"type":      esc.Type,
			"reason":    esc.Reason,
			"options":   esc.Options,
		}, params.signalEventData))
		if err := dispatchSignalHandlers(dispatchSignalInput{
			Store:         cfg.Store,
			Session:       cfg.Session,
			Stage:         params.hookStageName,
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
			return iterationOutcome{}, fmt.Errorf("runner: dispatch escalate handlers: %w", err)
		}

		outcome.stop = true
		outcome.status = store.StatusPaused
		outcome.reason = "escalation: " + esc.Reason
		return outcome, nil
	}

	if params.termination.FailOnAgentError && strings.EqualFold(strings.TrimSpace(iterResult.Decision), "error") {
		storeMarkFailed(ctx, cfg, "agent_error", fmt.Sprintf("agent requested error at stage %s iteration %d", params.hookStageName, i))
		params.hookCtx.SetStatus("failed")
		params.hookCtx.Fire(ctx, "on_failure")

		outcome.stop = true
		outcome.status = store.StatusFailed
		outcome.err = "agent requested error"
		return outcome, nil
	}

	if params.termination.Judgment != nil &&
		params.termination.Judgment.Strategy != nil &&
		params.termination.Judgment.Evaluator != nil &&
		!params.termination.Judgment.Strategy.InFallback() {
		judgeStop, judgeReason := params.termination.Judgment.Strategy.ShouldStop(
			ctx,
			i,
			params.termination.Judgment.Evaluator,
			params.termination.Judgment.Summaries,
		)

		emitEvent(ctx, cfg, store.TypeJudgeVerdict, params.cursorJSON, mergeEventData(map[string]any{
			"iteration":         i,
			"consecutive_stops": params.termination.Judgment.Strategy.ConsecutiveStops(),
			"in_fallback":       params.termination.Judgment.Strategy.InFallback(),
			"stop":              judgeStop,
		}, params.iterationEventData))

		if params.termination.Judgment.Strategy.InFallback() {
			emitEvent(ctx, cfg, store.TypeJudgeFallback, params.cursorJSON, mergeEventData(map[string]any{
				"iteration": i,
				"reason":    "judge failed 3 consecutive times, falling back to fixed-iteration termination",
			}, params.iterationEventData))
		}

		if judgeStop {
			outcome.stop = true
			outcome.status = params.termination.StopStatus
			outcome.reason = judgeReason
			return outcome, nil
		}
	}

	if shouldStop, reason := params.termination.Fixed.ShouldStop(i, iterResult.Decision); shouldStop {
		outcome.stop = true
		outcome.status = params.termination.StopStatus
		outcome.reason = reason
		return outcome, nil
	}

	return outcome, nil
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

	// Create empty messages.jsonl so agents can find it via context.json.
	ensureMessagesFile(cfg.RunDir)

	// Initialize hook context — accumulates state, available to all hooks.
	hc := NewHookContext(cfg)
	hc.SetStage(cfg.StageName)
	hc.SetContext(cfg.InjectedContext)
	hc.Fire(ctx, "pre_session")

	fixed := termination.NewFixed(termination.FixedConfig{Iterations: &cfg.Iterations})
	stageConfig := buildStageConfig(cfg)

	// Initialize judgment strategy if a judge provider is configured.
	var judgmentCfg *iterationJudgmentConfig
	if cfg.JudgeProvider != nil {
		judgmentCfg = &iterationJudgmentConfig{
			Strategy: termination.NewJudgment(termination.JudgmentConfig{
				ConsensusRequired: cfg.JudgeConsensus,
				MinIterations:     cfg.JudgeMinIterations,
			}),
			Evaluator: judge.New(judge.Config{
				Provider:   cfg.JudgeProvider,
				Model:      cfg.JudgeModel,
				MaxRetries: cfg.JudgeMaxRetries,
			}),
		}
	}

	completed := 0
	spawnedChildren := 0
	injectedContext := cfg.InjectedContext // seeded from resume --context or inject signal

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
			hc.SetIteration(completed)
			hc.SetStatus("failed")
			hc.Fire(context.Background(), "on_failure")
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

		outcome, err := executeIteration(ctx, iterationParams{
			cfg:                     cfg,
			hookCtx:                 hc,
			iteration:               i,
			providerFailureHookIter: i,
			stageConfig:             stageConfig,
			promptTemplate:          cfg.PromptTemplate,
			storeStageName:          cfg.StageName,
			hookStageName:           cfg.StageName,
			cursorJSON:              marshalCursorJSON(i, cfg.Provider.Name(), "", 0),
			outputErrorMessage:      fmt.Sprintf("write iteration %d output", i),
			injectedContext:         injectedContext,
			spawnedChildren:         spawnedChildren,
			retry: iterationRetryConfig{
				MaxAttempts:           retryMaxAttempts(cfg),
				Backoff:               retryBackoff(cfg),
				OnExhausted:           cfg.RetryOnExhausted,
				CountExhaustedFailure: 1,
			},
			termination: iterationTerminationConfig{
				Fixed:      fixed,
				Judgment:   judgmentCfg,
				StopStatus: store.StatusCompleted,
			},
		})
		if err != nil {
			return Result{}, err
		}

		completed += outcome.completedDelta
		injectedContext = outcome.injectedContext
		spawnedChildren = outcome.spawnedChildren

		if outcome.stop {
			if outcome.status == store.StatusCompleted {
				finishSession(ctx, cfg, completed, outcome.reason)
			}
			hc.SetIteration(completed)
			hc.SetStatus(outcome.status)
			if outcome.status == store.StatusCompleted {
				hc.Fire(ctx, "post_session")
			}
			return Result{
				Iterations: completed,
				Status:     outcome.status,
				Reason:     outcome.reason,
				Error:      outcome.err,
			}, nil
		}
	}

	// All iterations complete.
	reason := fmt.Sprintf("Completed %d iterations (max: %d)", completed, fixed.Target())
	finishSession(ctx, cfg, completed, reason)
	hc.SetIteration(completed)
	hc.SetStatus("completed")
	hc.Fire(ctx, "post_session")
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

	// Create empty messages.jsonl so agents can find it via context.json.
	ensureMessagesFile(cfg.RunDir)

	// Initialize hook context for pipeline run.
	hc := NewHookContext(cfg)
	hc.SetContext(cfg.InjectedContext)
	firstStage := nodes[0].StageName
	if firstStage == "" {
		firstStage = nodes[0].ID
	}
	hc.SetStage(firstStage)
	hc.Fire(ctx, "pre_session")

	completed := 0
	spawnedChildren := 0
	injectedContext := cfg.InjectedContext

	for nodeIdx, node := range nodes {
		// Handle swarm nodes.
		if node.Swarm != nil {
			_ = cfg.Store.UpdateSession(ctx, cfg.Session, map[string]any{
				"current_stage": node.ID,
				"node_id":       node.ID,
			})

			hc.SetStage(node.ID)
			hc.Fire(ctx, "pre_stage")

			swarmResult, swarmErr := executeSwarmNode(ctx, cfg, node, nodeIdx, injectedContext, pipelineName)
			if swarmErr != nil {
				storeMarkFailed(ctx, cfg, "swarm_error", swarmErr.Error())
				hc.SetStage(node.ID)
				hc.SetIteration(completed)
				hc.SetStatus("failed")
				hc.Fire(ctx, "on_failure")
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
			hc.SetStage(node.ID)
			hc.SetIteration(completed)
			hc.Fire(ctx, "post_stage")
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

		hc.SetStage(node.StageName)
		hc.Fire(ctx, "pre_stage")

		fixed := termination.NewFixed(termination.FixedConfig{Iterations: &node.Iterations})
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
				hc.SetStage(node.StageName)
				hc.SetIteration(completed)
				hc.SetStatus("failed")
				hc.Fire(context.Background(), "on_failure")
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

			cursorJSON := marshalCursorJSON(i, cfg.Provider.Name(), node.ID, nodeIdx+1)
			outcome, err := executeIteration(ctx, iterationParams{
				cfg:                     nodeCfg,
				hookCtx:                 hc,
				iteration:               i,
				providerFailureHookIter: completed,
				stageConfig:             stageConfig,
				promptTemplate:          nodeCfg.PromptTemplate,
				storeStageName:          node.ID,
				hookStageName:           node.StageName,
				cursorJSON:              cursorJSON,
				iterationEventData: map[string]any{
					"stage":   node.StageName,
					"node_id": node.ID,
				},
				signalEventData: map[string]any{
					"stage":   node.StageName,
					"node_id": node.ID,
				},
				outputErrorMessage: fmt.Sprintf("write stage output for %s iteration %d", node.ID, i),
				injectedContext:    injectedContext,
				spawnedChildren:    spawnedChildren,
				contextOpts:        &apcontext.GenerateContextOpts{PipelineName: pipelineName},
				retry: iterationRetryConfig{
					MaxAttempts: 1,
				},
				termination: iterationTerminationConfig{
					Fixed:            fixed,
					FailOnAgentError: true,
				},
				onStartIterationError: func(err error) {
					emitEvent(ctx, cfg, store.TypeError, "{}", map[string]any{
						"error":     err.Error(),
						"type":      "store_tracking",
						"operation": "start_iteration",
						"iteration": i,
						"stage":     node.ID,
					})
				},
				onCompleteIterationError: func(err error) {
					emitEvent(ctx, cfg, store.TypeError, cursorJSON, map[string]any{
						"error":     err.Error(),
						"type":      "store_tracking",
						"operation": "complete_iteration",
						"iteration": i,
						"stage":     node.ID,
					})
				},
			})
			if err != nil {
				return Result{}, err
			}

			completed += outcome.completedDelta
			stageCompleted += outcome.completedDelta
			injectedContext = outcome.injectedContext
			spawnedChildren = outcome.spawnedChildren

			if outcome.stop {
				if outcome.status != "" {
					return Result{
						Iterations: completed,
						Status:     outcome.status,
						Reason:     outcome.reason,
						Error:      outcome.err,
					}, nil
				}
				break
			}
		}

		// Append stage boundary marker to session-scoped progress.
		appendStageBoundary(cfg.RunDir, node.StageName, stageCompleted)
		hc.SetStage(node.StageName)
		hc.SetIteration(stageCompleted)
		hc.Fire(ctx, "post_stage")

		// Mark stage completed in stages_json.
		markStageCompleted(ctx, cfg, nodeIdx)
	}

	reason := fmt.Sprintf("Completed %d iterations across %d stages", completed, len(nodes))
	finishSession(ctx, cfg, completed, reason)
	hc.SetIteration(completed)
	hc.SetStatus("completed")
	hc.Fire(ctx, "post_session")
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
				ID:     nodeID,
				Index:  idx,
				Inputs: node.Inputs,
				Swarm:  node.Swarm,
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
func executeSwarmNode(ctx context.Context, cfg Config, node pipelineRunNode, nodeIdx int, injectedContext string, pipelineName string) (swarmNodeResult, error) {
	if node.Swarm == nil {
		return swarmNodeResult{}, fmt.Errorf("runner: node %q is not a swarm node", node.ID)
	}

	// Capture git HEAD once before any provider runs to avoid race
	// conditions between concurrent providers in the same worktree.
	blockPreHead := ""
	if isGitRepo(cfg.WorkDir) {
		blockPreHead = gitHead(cfg.WorkDir)
	}

	// Create the swarm executor with hook context for iteration hooks.
	executor := &swarmExecutor{
		cfg:          cfg,
		blockID:      node.ID,
		hookCtx:      NewHookContext(cfg),
		blockPreHead: blockPreHead,
		pipelineName: pipelineName,
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
	// Prefer full stdout so subsequent iterations see complete context,
	// not just the one-line summary from ap-result.
	content := strings.TrimSpace(provResult.Stdout)
	if content == "" {
		content = strings.TrimSpace(provResult.Output)
	}
	if content == "" {
		content = strings.TrimSpace(iterResult.Summary)
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

// ensureMessagesFile creates an empty messages.jsonl in the run directory
// if it doesn't already exist. Best-effort: errors are silently ignored.
func ensureMessagesFile(runDir string) {
	path := filepath.Join(runDir, "messages.jsonl")
	if _, err := os.Stat(path); err == nil {
		return // already exists
	}
	_ = os.WriteFile(path, []byte{}, 0o644)
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

// executeWithTimeout wraps provider execution with an optional timeout.
// When timeout is zero or negative, the parent context is used as-is.
func executeWithTimeout(ctx context.Context, prov provider.Provider, req provider.Request, timeout time.Duration) (provider.Result, error) {
	if timeout <= 0 {
		return prov.Execute(ctx, req)
	}
	iterCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := prov.Execute(iterCtx, req)
	if err != nil && iterCtx.Err() != nil && ctx.Err() == nil {
		// The iteration timed out but the session context is still alive.
		return result, fmt.Errorf("iteration timed out after %s: %w", timeout, err)
	}
	return result, err
}
