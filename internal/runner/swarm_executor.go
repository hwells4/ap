package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/compile"
	apcontext "github.com/hwells4/ap/internal/context"
	"github.com/hwells4/ap/internal/extract"
	"github.com/hwells4/ap/internal/swarm"
	"github.com/hwells4/ap/internal/resolve"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
	"github.com/hwells4/ap/internal/termination"
	"github.com/hwells4/ap/pkg/provider"
)

// swarmExecutor implements swarm.Executor by bridging to the runner's
// provider execution infrastructure.
type swarmExecutor struct {
	cfg     Config // runner.Config (store, workdir, etc.)
	blockID string
}

// Execute runs a single stage for a single provider instance inside a swarm block.
func (e *swarmExecutor) Execute(ctx context.Context, req swarm.ExecuteRequest) (swarm.StageResult, error) {
	// Resolve the provider type from the instance name.
	prov, err := e.resolveProvider(req.Provider)
	if err != nil {
		return swarm.StageResult{}, fmt.Errorf("resolve provider %q: %w", req.Provider.Name, err)
	}

	// Resolve stage definition.
	var promptTemplate string
	var outputPath string
	def, err := stage.ResolveStage(req.Stage.Stage, stage.ResolveOptions{
		ProjectRoot: e.cfg.WorkDir,
	})
	if err == nil {
		promptBytes, readErr := def.ReadPrompt()
		if readErr != nil {
			return swarm.StageResult{}, fmt.Errorf("read prompt for %q: %w", req.Stage.Stage, readErr)
		}
		promptTemplate = string(promptBytes)
		outputPath = def.ReadOutputPath()
	} else if e.cfg.PromptTemplate != "" {
		// Fall back to runner-level prompt template when stage files are unavailable.
		promptTemplate = e.cfg.PromptTemplate
	} else {
		return swarm.StageResult{}, fmt.Errorf("resolve stage %q: %w", req.Stage.Stage, err)
	}

	// Determine iteration count.
	runs := req.Stage.Runs
	if runs <= 0 {
		runs = 1
	}

	stageIndex := req.StageIndex
	stageName := swarmStageName(req.Stage, req.StageIndex)

	// Compound stage name for store recording.
	compoundStageName := fmt.Sprintf("swarm:%s:%s:%s", e.blockID, req.Provider.Name, stageName)

	stageConfig := apcontext.StageConfig{
		ID:            stageName,
		Name:          stageName,
		Index:         &stageIndex,
		MaxIterations: &runs,
		OutputPath:    outputPath,
	}

	fixed := termination.NewFixed(termination.FixedConfig{Iterations: &runs})
	injectedContext := ""
	var latestOutput string

	for i := 1; i <= fixed.Target(); i++ {
		if err := ctx.Err(); err != nil {
			return swarm.StageResult{
				Status:     "failed",
				Iterations: i - 1,
			}, nil
		}

		iterStartTime := time.Now()

		// Record iteration start in store.
		_ = e.cfg.Store.StartIteration(ctx, store.IterationInput{
			SessionName:  e.cfg.Session,
			StageName:    compoundStageName,
			Iteration:    i,
			ProviderName: prov.Name(),
		})

		// Capture git HEAD before execution.
		preHead := ""
		if isGitRepo(e.cfg.WorkDir) {
			preHead = gitHead(e.cfg.WorkDir)
		}

		// Write provider-scoped history.
		historyPath := filepath.Join(req.StageDir, "iterations", fmt.Sprintf("%03d", i), "history.md")
		writeProviderHistory(ctx, e.cfg.Store, e.cfg.Session, compoundStageName, historyPath)

		// Generate context.json into the provider's stage directory.
		ctxPath, err := apcontext.GenerateContext(e.cfg.Session, i, stageConfig, req.ProviderDir, nil)
		if err != nil {
			return swarm.StageResult{}, fmt.Errorf("generate context for iteration %d: %w", i, err)
		}

		// Resolve prompt template with variables.
		prompt := resolveSwarmPrompt(promptTemplate, ctxPath, e.cfg.Session, i, stageConfig, injectedContext)
		injectedContext = "" // consumed

		// Read output path from context.
		ctxVars, _ := resolve.VarsFromContext(ctxPath)

		// Build provider request.
		provReq := provider.Request{
			Prompt:  prompt,
			Model:   req.Provider.Model,
			WorkDir: e.cfg.WorkDir,
			Env:     buildSwarmEnv(e.cfg, req, i),
		}
		if provReq.Model == "" {
			provReq.Model = e.cfg.Model
		}

		// Execute provider.
		provResult, provErr := prov.Execute(ctx, provReq)
		if provErr != nil {
			_ = e.cfg.Store.CompleteIteration(ctx, store.IterationComplete{
				SessionName:  e.cfg.Session,
				StageName:    compoundStageName,
				Iteration:    i,
				Decision:     "error",
				Summary:      provErr.Error(),
				ExitCode:     provResult.ExitCode,
				ProviderName: prov.Name(),
				DurationMS:   time.Since(iterStartTime).Milliseconds(),
			})
			return swarm.StageResult{
				Status:     "failed",
				Iterations: i,
			}, fmt.Errorf("provider execution failed at iteration %d: %w", i, provErr)
		}

		// Extract result.
		iterResult, _, _ := extract.Extract(provResult.Stdout, provResult.ExitCode)

		// Build work manifest from git changes.
		manifest := buildWorkManifest(e.cfg.WorkDir, preHead)
		manifestJSON, _ := json.Marshal(manifest)
		diffStat := ""
		if manifest.Git != nil {
			diffStat = manifest.Git.DiffStat
		}

		// Write iteration output.
		if err := writeIterationOutput(ctxVars.OUTPUT, iterResult, provResult, diffStat); err != nil {
			return swarm.StageResult{}, fmt.Errorf("write output for iteration %d: %w", i, err)
		}
		latestOutput = ctxVars.OUTPUT

		// Complete iteration in store.
		signalsJSON, _ := json.Marshal(iterResult.Signals)
		_ = e.cfg.Store.CompleteIteration(ctx, store.IterationComplete{
			SessionName:  e.cfg.Session,
			StageName:    compoundStageName,
			Iteration:    i,
			Decision:     iterResult.Decision,
			Summary:      iterResult.Summary,
			ExitCode:     provResult.ExitCode,
			SignalsJSON:  string(signalsJSON),
			Stdout:       provResult.Stdout,
			Stderr:       provResult.Stderr,
			ContextJSON:  string(manifestJSON),
			ProviderName: prov.Name(),
			DurationMS:   time.Since(iterStartTime).Milliseconds(),
		})

		// Handle inject signal.
		if iterResult.Signals.Inject != "" {
			injectedContext = iterResult.Signals.Inject
		}

		// Check for stop/error decisions.
		decision := strings.ToLower(strings.TrimSpace(iterResult.Decision))
		if decision == "error" || decision == "stop" {
			return swarm.StageResult{
				LatestOutput:      latestOutput,
				Status:            "completed",
				Iterations:        i,
				TerminationReason: decision,
			}, nil
		}
	}

	return swarm.StageResult{
		LatestOutput:      latestOutput,
		Status:            "completed",
		Iterations:        fixed.Target(),
		TerminationReason: "fixed",
	}, nil
}

// resolveProvider creates a provider instance from the provider config.
// It uses the runner's main provider as the base and applies model overrides.
func (e *swarmExecutor) resolveProvider(spec compile.ProviderConfig) (provider.Provider, error) {
	// Strip instance suffix to get the canonical provider type.
	baseName := swarm.StripInstanceSuffix(spec.Name)
	canonical := provider.NormalizeName(baseName)

	// Look up from the global registry first.
	if p, ok := provider.Get(canonical); ok {
		return p, nil
	}

	// Fall back to the runner's configured provider.
	// In swarm blocks, the runner's provider is the execution engine;
	// the instance name (e.g. "claude-2") is just for directory isolation.
	if e.cfg.Provider != nil {
		return e.cfg.Provider, nil
	}

	return nil, fmt.Errorf("unknown provider type: %s (canonical: %s)", baseName, canonical)
}

// swarmStageName returns the stage name for a swarm stage.
func swarmStageName(ps compile.SwarmStage, index int) string {
	if name := strings.TrimSpace(ps.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(ps.ID); id != "" {
		return id
	}
	if stageName := strings.TrimSpace(ps.Stage); stageName != "" {
		return stageName
	}
	return fmt.Sprintf("stage-%d", index+1)
}

// resolveSwarmPrompt resolves a prompt template for a swarm provider iteration.
func resolveSwarmPrompt(template, ctxPath, session string, iteration int, sc apcontext.StageConfig, injectedContext string) string {
	vars, err := resolve.VarsFromContext(ctxPath)
	if err != nil {
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

// writeProviderHistory writes history.md scoped to a specific compound stage name.
func writeProviderHistory(ctx context.Context, s *store.Store, session, compoundStageName, path string) {
	rows, err := s.GetIterations(ctx, session, compoundStageName)
	if err != nil || len(rows) == 0 {
		return
	}

	var buf strings.Builder
	buf.WriteString("# Provider History\n\n")

	for _, r := range rows {
		if r.Status != "completed" && r.Status != "failed" {
			continue
		}
		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			summary = "(no summary)"
		}
		if len(summary) > 200 {
			summary = summary[:200] + "..."
		}
		buf.WriteString(fmt.Sprintf("- **Iteration %d** [%s]: %s\n", r.Iteration, r.Decision, summary))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(buf.String()), 0o644)
}

// buildSwarmEnv constructs environment variables for a swarm provider execution.
func buildSwarmEnv(cfg Config, req swarm.ExecuteRequest, iteration int) map[string]string {
	env := make(map[string]string, len(cfg.Env)+6)
	for k, v := range cfg.Env {
		env[k] = v
	}
	env["AP_AGENT"] = "1"
	env["AP_SESSION"] = cfg.Session
	env["AP_STAGE"] = req.Stage.Stage
	env["AP_ITERATION"] = strconv.Itoa(iteration)
	env["AP_PROVIDER"] = req.Provider.Name
	env["AP_SWARM"] = "1"
	return env
}
