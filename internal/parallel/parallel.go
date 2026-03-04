// Package parallel runs provider-isolated parallel blocks.
package parallel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hwells4/ap/internal/compile"
)

const (
	providerStateFile = "state.json"
	providerProgFile  = "progress.md"
	manifestFile      = "manifest.json"
)

type providerStatus string

const (
	statusRunning   providerStatus = "running"
	statusCompleted providerStatus = "completed"
	statusFailed    providerStatus = "failed"
)

// Config controls one parallel block execution.
type Config struct {
	RunDir     string
	BlockID    string
	BlockIndex int
	Providers  []compile.ProviderConfig
	Stages     []compile.ParallelStage
	Resume     bool
	Executor   Executor
}

// Executor runs one stage for one provider inside the block.
type Executor interface {
	Execute(ctx context.Context, req ExecuteRequest) (StageResult, error)
}

// ExecutorFunc adapts a function to the Executor interface.
type ExecutorFunc func(ctx context.Context, req ExecuteRequest) (StageResult, error)

// Execute calls f(ctx, req).
func (f ExecutorFunc) Execute(ctx context.Context, req ExecuteRequest) (StageResult, error) {
	return f(ctx, req)
}

// ExecuteRequest is one stage execution request.
type ExecuteRequest struct {
	BlockDir      string
	Provider      compile.ProviderConfig
	Stage         compile.ParallelStage
	StageIndex    int
	ProviderDir   string
	StageDir      string
	ProgressPath  string
	ProviderState string
}

// StageResult is the provider/stage output saved into the block manifest.
type StageResult struct {
	LatestOutput      string   `json:"latest_output"`
	Status            string   `json:"status"`
	Iterations        int      `json:"iterations"`
	TerminationReason string   `json:"termination_reason,omitempty"`
	History           []string `json:"history,omitempty"`
}

// ProviderResult captures one provider's overall block status.
type ProviderResult struct {
	Name         string
	Directory    string
	ProgressPath string
	StatePath    string
	Status       string
	Skipped      bool
	Error        string
	Stages       map[string]StageResult
}

// Result captures one parallel block execution.
type Result struct {
	BlockDir      string
	ProvidersRoot string
	ManifestPath  string
	Providers     map[string]ProviderResult
}

type providerState struct {
	Provider        string   `json:"provider"`
	Status          string   `json:"status"`
	CurrentStage    string   `json:"current_stage,omitempty"`
	CompletedStages []string `json:"completed_stages,omitempty"`
	Error           string   `json:"error,omitempty"`
	UpdatedAt       string   `json:"updated_at"`
}

type manifestBlock struct {
	Block struct {
		Name string `json:"name"`
	} `json:"block"`
	Providers map[string]map[string]StageResult `json:"providers"`
}

type providerWork struct {
	spec         compile.ProviderConfig
	name         string
	dir          string
	progressPath string
	statePath    string
}

// Run executes a parallel block.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if strings.TrimSpace(cfg.RunDir) == "" {
		return Result{}, fmt.Errorf("parallel: run dir is required")
	}
	if cfg.Executor == nil {
		return Result{}, fmt.Errorf("parallel: executor is required")
	}
	if len(cfg.Providers) == 0 {
		return Result{}, fmt.Errorf("parallel: at least one provider is required")
	}
	if len(cfg.Stages) == 0 {
		return Result{}, fmt.Errorf("parallel: at least one stage is required")
	}

	blockDirName := formatBlockDirName(cfg.BlockIndex, cfg.BlockID)
	blockDir := filepath.Join(cfg.RunDir, blockDirName)
	providersRoot := filepath.Join(blockDir, "providers")
	if err := os.MkdirAll(providersRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("parallel: create providers root: %w", err)
	}

	out := Result{
		BlockDir:      blockDir,
		ProvidersRoot: providersRoot,
		ManifestPath:  filepath.Join(blockDir, manifestFile),
		Providers:     make(map[string]ProviderResult, len(cfg.Providers)),
	}

	workItems := make([]providerWork, 0, len(cfg.Providers))
	for _, providerSpec := range cfg.Providers {
		name := normalizedProviderName(providerSpec.Name)
		if name == "" {
			return Result{}, fmt.Errorf("parallel: provider name is required")
		}

		providerDir := filepath.Join(providersRoot, name)
		progressPath := filepath.Join(providerDir, providerProgFile)
		statePath := filepath.Join(providerDir, providerStateFile)
		if err := os.MkdirAll(providerDir, 0o755); err != nil {
			return Result{}, fmt.Errorf("parallel: create provider dir: %w", err)
		}
		if err := ensureProgressFile(progressPath); err != nil {
			return Result{}, err
		}

		work := providerWork{
			spec:         providerSpec,
			name:         name,
			dir:          providerDir,
			progressPath: progressPath,
			statePath:    statePath,
		}
		if cfg.Resume {
			prev, err := readProviderState(statePath)
			if err == nil && strings.EqualFold(prev.Status, string(statusCompleted)) {
				out.Providers[name] = ProviderResult{
					Name:         name,
					Directory:    providerDir,
					ProgressPath: progressPath,
					StatePath:    statePath,
					Status:       prev.Status,
					Skipped:      true,
					Stages:       map[string]StageResult{},
				}
				continue
			}
		}
		workItems = append(workItems, work)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		runErrs []error
	)

	for _, work := range workItems {
		work := work
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := runProvider(ctx, cfg.Executor, blockDir, work, cfg.Stages)

			mu.Lock()
			defer mu.Unlock()
			out.Providers[work.name] = result
			if err != nil {
				runErrs = append(runErrs, err)
			}
		}()
	}

	wg.Wait()

	if err := writeManifest(out.ManifestPath, strings.TrimSpace(cfg.BlockID), out.Providers); err != nil {
		return out, err
	}

	if len(runErrs) > 0 {
		return out, errors.Join(runErrs...)
	}
	return out, nil
}

func runProvider(
	ctx context.Context,
	executor Executor,
	blockDir string,
	work providerWork,
	stages []compile.ParallelStage,
) (ProviderResult, error) {
	result := ProviderResult{
		Name:         work.name,
		Directory:    work.dir,
		ProgressPath: work.progressPath,
		StatePath:    work.statePath,
		Status:       string(statusRunning),
		Stages:       map[string]StageResult{},
	}

	if err := writeProviderState(work.statePath, providerState{
		Provider:  work.name,
		Status:    string(statusRunning),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return result, err
	}

	completedStages := make([]string, 0, len(stages))
	for idx, stage := range stages {
		stageKey := parallelStageKey(stage, idx)
		if err := writeProviderState(work.statePath, providerState{
			Provider:        work.name,
			Status:          string(statusRunning),
			CurrentStage:    stageKey,
			CompletedStages: append([]string(nil), completedStages...),
			UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return result, err
		}

		stageDir := filepath.Join(work.dir, stageDirectoryName(idx, stage))
		if err := os.MkdirAll(stageDir, 0o755); err != nil {
			return result, fmt.Errorf("parallel: create stage dir: %w", err)
		}
		stageRes, err := executor.Execute(ctx, ExecuteRequest{
			BlockDir:      blockDir,
			Provider:      work.spec,
			Stage:         stage,
			StageIndex:    idx,
			ProviderDir:   work.dir,
			StageDir:      stageDir,
			ProgressPath:  work.progressPath,
			ProviderState: work.statePath,
		})
		if err != nil {
			result.Status = string(statusFailed)
			result.Error = err.Error()
			_ = writeProviderState(work.statePath, providerState{
				Provider:        work.name,
				Status:          string(statusFailed),
				CurrentStage:    stageKey,
				CompletedStages: append([]string(nil), completedStages...),
				Error:           err.Error(),
				UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
			})
			result.Stages[stageKey] = StageResult{
				Status:     string(statusFailed),
				Iterations: effectiveStageRuns(stage),
			}
			return result, fmt.Errorf("parallel: provider %s stage %s: %w", work.name, stageKey, err)
		}
		if strings.TrimSpace(stageRes.Status) == "" {
			stageRes.Status = string(statusCompleted)
		}
		if stageRes.Iterations <= 0 {
			stageRes.Iterations = effectiveStageRuns(stage)
		}
		result.Stages[stageKey] = stageRes
		completedStages = append(completedStages, stageKey)
	}

	result.Status = string(statusCompleted)
	if err := writeProviderState(work.statePath, providerState{
		Provider:        work.name,
		Status:          string(statusCompleted),
		CompletedStages: completedStages,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return result, err
	}
	return result, nil
}

func writeManifest(path, blockID string, providers map[string]ProviderResult) error {
	manifest := manifestBlock{
		Providers: make(map[string]map[string]StageResult, len(providers)),
	}
	if strings.TrimSpace(blockID) == "" {
		manifest.Block.Name = "parallel"
	} else {
		manifest.Block.Name = strings.TrimSpace(blockID)
	}

	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		stageMap := make(map[string]StageResult, len(providers[key].Stages))
		stageKeys := make([]string, 0, len(providers[key].Stages))
		for stageKey := range providers[key].Stages {
			stageKeys = append(stageKeys, stageKey)
		}
		sort.Strings(stageKeys)
		for _, stageKey := range stageKeys {
			stageMap[stageKey] = providers[key].Stages[stageKey]
		}
		manifest.Providers[key] = stageMap
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("parallel: marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("parallel: write manifest: %w", err)
	}
	return nil
}

func ensureProgressFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		return fmt.Errorf("parallel: create progress file: %w", err)
	}
	return nil
}

func readProviderState(path string) (providerState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return providerState{}, err
	}
	var state providerState
	if err := json.Unmarshal(data, &state); err != nil {
		return providerState{}, err
	}
	return state, nil
}

func writeProviderState(path string, state providerState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("parallel: marshal provider state: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("parallel: write provider state: %w", err)
	}
	return nil
}

func effectiveStageRuns(stage compile.ParallelStage) int {
	if stage.Runs > 0 {
		return stage.Runs
	}
	return 1
}

func stageDirectoryName(index int, stage compile.ParallelStage) string {
	return fmt.Sprintf("stage-%02d-%s", index+1, sanitizeSegment(parallelStageKey(stage, index)))
}

func parallelStageKey(stage compile.ParallelStage, index int) string {
	if name := strings.TrimSpace(stage.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(stage.ID); id != "" {
		return id
	}
	if stageName := strings.TrimSpace(stage.Stage); stageName != "" {
		return stageName
	}
	return fmt.Sprintf("stage-%d", index+1)
}

func formatBlockDirName(index int, blockID string) string {
	if index < 0 {
		index = 0
	}
	base := fmt.Sprintf("parallel-%02d", index)
	if trimmed := strings.TrimSpace(blockID); trimmed != "" {
		return base + "-" + sanitizeSegment(trimmed)
	}
	return base
}

func normalizedProviderName(name string) string {
	return sanitizeSegment(strings.ToLower(strings.TrimSpace(name)))
}

func sanitizeSegment(value string) string {
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	return out
}
