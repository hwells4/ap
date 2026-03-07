package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/engine"
	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runner"
	"github.com/hwells4/ap/internal/runtarget"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/pkg/provider"
	"github.com/hwells4/ap/pkg/provider/claude"
	"github.com/hwells4/ap/pkg/provider/codex"
)

// RunRequestFile is the on-disk format for run_request.json.
type RunRequestFile struct {
	Session        string            `json:"session"`
	Stage          string            `json:"stage"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Iterations     int               `json:"iterations"`
	PromptTemplate string            `json:"prompt_template"`
	Pipeline       *compile.Pipeline `json:"pipeline,omitempty"`
	WorkDir        string            `json:"work_dir"`
	Env            map[string]string `json:"env"`
	RunDir         string            `json:"run_dir"`
	OnEscalate     string            `json:"on_escalate,omitempty"`
	ParentSession  string            `json:"parent_session,omitempty"`
	ProjectRoot    string            `json:"project_root,omitempty"`
	RepoRoot       string            `json:"repo_root,omitempty"`
	ConfigRoot     string            `json:"config_root,omitempty"`
	ProjectKey     string            `json:"project_key,omitempty"`
	TargetSource   string            `json:"target_source,omitempty"`
	OutputPath     string            `json:"output_path,omitempty"`
	SpawnDepth       int               `json:"spawn_depth,omitempty"`
	ContextOverride  string            `json:"context_override,omitempty"`
}

// WriteRunRequest atomically writes a run_request.json file.
func WriteRunRequest(path string, req RunRequestFile) error {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run request: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create run request dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write run request: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename run request: %w", err)
	}
	return nil
}

// ReadRunRequest reads and validates a run_request.json file.
func ReadRunRequest(path string) (RunRequestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunRequestFile{}, fmt.Errorf("read run request: %w", err)
	}
	var req RunRequestFile
	if err := json.Unmarshal(data, &req); err != nil {
		return RunRequestFile{}, fmt.Errorf("parse run request: %w", err)
	}
	if req.Session == "" {
		return RunRequestFile{}, fmt.Errorf("run request: missing session")
	}
	hasPipeline := req.Pipeline != nil && len(req.Pipeline.Nodes) > 0
	if req.Stage == "" && !hasPipeline {
		return RunRequestFile{}, fmt.Errorf("run request: missing stage")
	}
	if req.Provider == "" {
		return RunRequestFile{}, fmt.Errorf("run request: missing provider")
	}
	if req.Iterations <= 0 && !hasPipeline {
		return RunRequestFile{}, fmt.Errorf("run request: iterations must be positive, got %d", req.Iterations)
	}
	if req.RunDir == "" {
		return RunRequestFile{}, fmt.Errorf("run request: missing run_dir")
	}
	return req, nil
}

// runInternalRun handles the hidden `ap _run` entrypoint.
// Usage: ap _run --session NAME --request PATH [--resume]
func runInternalRun(args []string, deps cliDeps) int {
	var session, requestPath string
	var resume bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			if i+1 >= len(args) {
				return internalRunError(deps, "flag --session requires a value")
			}
			i++
			session = args[i]
		case "--request":
			if i+1 >= len(args) {
				return internalRunError(deps, "flag --request requires a value")
			}
			i++
			requestPath = args[i]
		case "--resume":
			resume = true
		default:
			return internalRunError(deps, fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	if session == "" {
		return internalRunError(deps, "missing required flag --session")
	}
	if requestPath == "" {
		return internalRunError(deps, "missing required flag --request")
	}

	// Open the store for _run.
	s, storeErr := openStore(deps, true)
	if storeErr != nil {
		return internalRunError(deps, fmt.Sprintf("open store: %v", storeErr))
	}
	if s == nil {
		return internalRunError(deps, "open store: store is not available")
	}
	defer s.Close()
	deps.store = s

	ctx := context.Background()

	// Try reading run_request from the store first, fall back to file.
	var req RunRequestFile
	row, getErr := s.GetSession(ctx, session)
	if getErr == nil && row.RunRequestJSON != "" && row.RunRequestJSON != "{}" {
		if parseErr := json.Unmarshal([]byte(row.RunRequestJSON), &req); parseErr != nil {
			return internalRunError(deps, fmt.Sprintf("parse store run request: %v", parseErr))
		}
	} else {
		var readErr error
		req, readErr = ReadRunRequest(requestPath)
		if readErr != nil {
			return internalRunError(deps, readErr.Error())
		}
	}

	// Verify session name matches.
	if req.Session != session {
		return internalRunError(deps, fmt.Sprintf(
			"session mismatch: flag says %q, request says %q", session, req.Session))
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(req.ProjectRoot)
	}
	target, targetErr := runtarget.NormalizeWithDefaults(runtarget.Target{
		ProjectRoot: workDir,
		RepoRoot:    strings.TrimSpace(req.RepoRoot),
		ConfigRoot:  strings.TrimSpace(req.ConfigRoot),
		ProjectKey:  strings.TrimSpace(req.ProjectKey),
		Source:      strings.TrimSpace(req.TargetSource),
	}, req.TargetSource)
	if targetErr != nil {
		return internalRunError(deps, fmt.Sprintf("resolve run target: %v", targetErr))
	}
	workDir = target.ProjectRoot

	// Acquire session lock.
	locksDir := filepath.Join(filepath.Dir(req.RunDir), "..", "locks")
	lk, lockErr := lock.Acquire(locksDir, session)
	if lockErr != nil {
		return renderError(deps, output.ExitLocked, output.NewError(
			"SESSION_LOCKED",
			lockErr.Error(),
			"",
			"ap _run --session NAME --request PATH",
			[]string{"ap kill " + session, "ap status " + session},
		))
	}
	defer lk.Release()

	// Resolve provider.
	eng := engine.New()
	prov, provErr := resolveProvider(eng, req.Provider)
	if provErr != nil {
		return internalRunError(deps, fmt.Sprintf("resolve provider %q: %v", req.Provider, provErr))
	}

	// Resolve prompt template if not already in request.
	// When a pipeline is set, the runner resolves per-stage prompts itself.
	promptTemplate := req.PromptTemplate
	if promptTemplate == "" && req.Pipeline == nil {
		def, stageErr := stage.ResolveStage(req.Stage, stage.ResolveOptions{
			ProjectRoot: workDir,
		})
		if stageErr != nil {
			return internalRunError(deps, fmt.Sprintf("resolve stage %q: %v", req.Stage, stageErr))
		}
		promptData, readErr := def.ReadPrompt()
		if readErr != nil {
			return internalRunError(deps, fmt.Sprintf("read prompt for stage %q: %v", req.Stage, readErr))
		}
		promptTemplate = string(promptData)
	}

	// On resume, clean up any iterations orphaned by a prior crash.
	if resume {
		if _, err := s.CleanOrphanedIterations(ctx, session); err != nil {
			return internalRunError(deps, fmt.Sprintf("clean orphaned iterations: %v", err))
		}
	}

	loadedConfig, cfgErr := config.Load("")
	if cfgErr != nil {
		return internalRunError(deps, fmt.Sprintf("load config: %v", cfgErr))
	}
	escalateHandlers := loadedConfig.SignalHandlers("escalate")
	if override := strings.TrimSpace(req.OnEscalate); override != "" {
		handler, err := parseOnEscalateOverride(override)
		if err != nil {
			return internalRunError(deps, fmt.Sprintf("parse --on-escalate override: %v", err))
		}
		escalateHandlers = append(escalateHandlers, handler)
	}
	limits := loadedConfig.RunnerLimits()

	// Resolve lifecycle hooks: stage > pipeline > config (most specific wins).
	hooks := resolveLifecycleHooks(loadedConfig, req.Pipeline, req.Stage, workDir)
	hookTimeout := loadedConfig.Hooks.Timeout

	// Build runner config.
	cfg := runner.Config{
		Session:              req.Session,
		RunDir:               req.RunDir,
		StageName:            req.Stage,
		Pipeline:             req.Pipeline,
		Provider:             prov,
		Iterations:           req.Iterations,
		PromptTemplate:       promptTemplate,
		OutputPath:           strings.TrimSpace(req.OutputPath),
		Model:                req.Model,
		WorkDir:              workDir,
		RunTarget:            target,
		Env:                  req.Env,
		OnEscalate:           req.OnEscalate,
		SpawnMaxChildren:     limits.MaxChildSessions,
		SpawnMaxDepth:        limits.MaxSpawnDepth,
		SpawnDepth:           req.SpawnDepth,
		InjectedContext:      req.ContextOverride,
		EscalateHandlers:     escalateHandlers,
		SpawnHandlers:        loadedConfig.SignalHandlers("spawn"),
		SignalHandlerTimeout: loadedConfig.Signals.HandlerTimeout,
		Store:                s,
		ParentSession:        strings.TrimSpace(req.ParentSession),
		Hooks:                hooks,
		HookTimeout:          hookTimeout,
		IterationTimeout:     limits.IterationTimeout,
	}

	// Execute runner.
	result, runErr := runner.Run(ctx, cfg)
	if runErr != nil {
		return internalRunError(deps, fmt.Sprintf("runner: %v", runErr))
	}

	// Output result.
	if deps.mode == output.ModeJSON {
		payload := map[string]any{
			"session":    session,
			"iterations": result.Iterations,
			"status":     string(result.Status),
			"reason":     result.Reason,
		}
		if result.Error != "" {
			payload["error"] = result.Error
		}
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, nil))
		if err != nil {
			return internalRunError(deps, err.Error())
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
	} else {
		_, _ = fmt.Fprintf(deps.stdout, "Session %q: %s (%d iterations, %s)\n",
			session, result.Status, result.Iterations, result.Reason)
		if result.Error != "" {
			_, _ = fmt.Fprintf(deps.stderr, "Error: %s\n", result.Error)
		}
	}

	if result.Status == "failed" {
		return output.ExitProviderError
	}
	return output.ExitSuccess
}

// availableProviders is the set of known provider names for error messages.
var availableProviders = []string{"claude", "codex"}

// resolveProvider creates and registers a provider by name.
func resolveProvider(eng *engine.Engine, name string) (provider.Provider, error) {
	normalized := provider.NormalizeName(name)
	switch normalized {
	case "claude":
		cp := claude.New()
		if err := eng.RegisterProvider("claude", cp); err != nil {
			return nil, err
		}
		return cp, nil
	case "codex":
		cp := codex.New()
		if err := eng.RegisterProvider("codex", cp); err != nil {
			return nil, err
		}
		return cp, nil
	default:
		return nil, fmt.Errorf("unknown provider %q; available: %s", name, strings.Join(availableProviders, ", "))
	}
}

// validateProviderName checks if the provider is known and returns a structured error if not.
func validateProviderName(name string) *output.ErrorResponse {
	normalized := provider.NormalizeName(name)
	for _, known := range availableProviders {
		if normalized == known {
			return nil
		}
	}
	errResp := output.NewError(
		"UNKNOWN_PROVIDER",
		fmt.Sprintf("unknown provider %q", name),
		fmt.Sprintf("Provider %q is not registered. Available providers: %s.", name, strings.Join(availableProviders, ", ")),
		"ap run <spec> <session> --provider NAME",
		[]string{
			"ap run ralph my-session --provider claude",
			"ap run ralph my-session --provider codex",
		},
	)
	errResp.Error.Available = map[string]any{"providers": availableProviders}
	return &errResp
}

// validateModelForProvider checks if the model is valid for the resolved provider.
// Returns nil if the model is empty (provider will use its default) or valid.
func validateModelForProvider(model, providerName string) *output.ErrorResponse {
	if model == "" {
		return nil
	}

	normalized := provider.NormalizeName(providerName)
	var supported []string
	var resolvedModel string

	switch normalized {
	case "claude":
		resolvedModel = claude.ResolveModel(model)
		supported = claude.SupportedModels
	case "codex":
		resolvedModel = codex.BaseModel(model) // strip reasoning suffix
		supported = codex.SupportedModels
	default:
		return nil // unknown provider — skip model validation
	}

	for _, m := range supported {
		if strings.EqualFold(resolvedModel, m) {
			return nil
		}
	}

	errResp := output.NewError(
		"UNKNOWN_MODEL",
		fmt.Sprintf("unknown model %q for provider %q", model, normalized),
		fmt.Sprintf("Model %q is not supported by provider %q. Available models: %s.", model, normalized, strings.Join(supported, ", ")),
		"ap run <spec> <session> -m MODEL --provider NAME",
		[]string{
			fmt.Sprintf("ap run ralph my-session -m %s", supported[0]),
		},
	)
	errResp.Error.Available = map[string]any{"models": supported}
	return &errResp
}

func internalRunError(deps cliDeps, msg string) int {
	_, _ = fmt.Fprintf(deps.stderr, "_run: %s\n", msg)
	return output.ExitGeneralError
}

func parseOnEscalateOverride(raw string) (config.SignalHandler, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return config.SignalHandler{}, fmt.Errorf("value is required")
	}

	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return config.SignalHandler{}, fmt.Errorf("expected format TYPE:VALUE")
	}
	kind := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if value == "" {
		return config.SignalHandler{}, fmt.Errorf("override target is required")
	}

	switch kind {
	case "webhook":
		return config.SignalHandler{
			Type:    "webhook",
			URL:     value,
			Headers: map[string]string{},
		}, nil
	case "exec":
		argv := strings.Fields(value)
		if len(argv) == 0 {
			return config.SignalHandler{}, fmt.Errorf("exec override must include a command")
		}
		return config.SignalHandler{
			Type:    "exec",
			Argv:    argv,
			Headers: map[string]string{},
		}, nil
	default:
		return config.SignalHandler{}, fmt.Errorf("unsupported override type %q (expected webhook or exec)", kind)
	}
}

// resolveLifecycleHooks builds LifecycleHooks with precedence:
// stage hooks > pipeline hooks > global config hooks.
// For each hook point, most-specific non-empty value wins.
func resolveLifecycleHooks(cfg config.Config, pipeline *compile.Pipeline, stageName, projectRoot string) runner.LifecycleHooks {
	globalHooks := cfg.WatchHooks()
	hooks := runner.LifecycleHooks{
		PreSession:    globalHooks.PreSession,
		PreIteration:  globalHooks.PreIteration,
		PreStage:      globalHooks.PreStage,
		PostIteration: globalHooks.PostIteration,
		PostStage:     globalHooks.PostStage,
		PostSession:   globalHooks.PostSession,
		OnFailure:     globalHooks.OnFailure,
	}

	// Pipeline hooks override global.
	if pipeline != nil && len(pipeline.Hooks) > 0 {
		hooks.ApplyOverrides(pipeline.Hooks)
	}

	// Stage hooks override pipeline and global (single-stage runs only).
	if strings.TrimSpace(stageName) != "" && pipeline == nil {
		def, err := stage.ResolveStage(stageName, stage.ResolveOptions{
			ProjectRoot: projectRoot,
		})
		if err == nil {
			hooks.ApplyOverrides(def.ReadHooks())
		}
	}

	return hooks
}
