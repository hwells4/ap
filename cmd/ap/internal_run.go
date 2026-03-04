package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/engine"
	"github.com/hwells4/ap/internal/lock"
	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/runner"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/pkg/provider"
	"github.com/hwells4/ap/pkg/provider/claude"
)

// RunRequestFile is the on-disk format for run_request.json.
type RunRequestFile struct {
	Session        string            `json:"session"`
	Stage          string            `json:"stage"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model"`
	Iterations     int               `json:"iterations"`
	PromptTemplate string            `json:"prompt_template"`
	WorkDir        string            `json:"work_dir"`
	Env            map[string]string `json:"env"`
	RunDir         string            `json:"run_dir"`
	OnEscalate     string            `json:"on_escalate,omitempty"`
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
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
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
	if req.Stage == "" {
		return RunRequestFile{}, fmt.Errorf("run request: missing stage")
	}
	if req.Provider == "" {
		return RunRequestFile{}, fmt.Errorf("run request: missing provider")
	}
	if req.Iterations <= 0 {
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

	// Read run request.
	req, err := ReadRunRequest(requestPath)
	if err != nil {
		return internalRunError(deps, err.Error())
	}

	// Verify session name matches.
	if req.Session != session {
		return internalRunError(deps, fmt.Sprintf(
			"session mismatch: flag says %q, request says %q", session, req.Session))
	}

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
	promptTemplate := req.PromptTemplate
	if promptTemplate == "" {
		def, stageErr := stage.ResolveStage(req.Stage, stage.ResolveOptions{
			ProjectRoot: req.WorkDir,
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

	_ = resume // TODO: resume support reads existing state

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

	// Build runner config.
	cfg := runner.Config{
		Session:              req.Session,
		RunDir:               req.RunDir,
		StageName:            req.Stage,
		Provider:             prov,
		Iterations:           req.Iterations,
		PromptTemplate:       promptTemplate,
		Model:                req.Model,
		WorkDir:              req.WorkDir,
		Env:                  req.Env,
		OnEscalate:           req.OnEscalate,
		SpawnMaxChildren:     limits.MaxChildSessions,
		SpawnMaxDepth:        limits.MaxSpawnDepth,
		EscalateHandlers:     escalateHandlers,
		SpawnHandlers:        loadedConfig.SignalHandlers("spawn"),
		SignalHandlerTimeout: loadedConfig.Signals.HandlerTimeout,
	}

	// Execute runner.
	ctx := context.Background()
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

// resolveProvider creates and registers a provider by name.
func resolveProvider(eng *engine.Engine, name string) (provider.Provider, error) {
	switch name {
	case "claude":
		cp := claude.New()
		if err := eng.RegisterProvider("claude", cp); err != nil {
			return nil, err
		}
		return cp, nil
	default:
		return nil, fmt.Errorf("unknown provider %q; supported: claude", name)
	}
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
