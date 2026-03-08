// Package swarm runs provider-isolated swarm blocks.
package swarm

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
	resumeFile        = "resume.json"
)

type providerStatus string

const (
	statusRunning   providerStatus = "running"
	statusCompleted providerStatus = "completed"
	statusFailed    providerStatus = "failed"
)

// Config controls one swarm block execution.
type Config struct {
	RunDir     string
	BlockID    string
	BlockIndex int
	Providers  []compile.ProviderConfig
	Stages     []compile.SwarmStage
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
	Stage         compile.SwarmStage
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

// Result captures one swarm block execution.
type Result struct {
	BlockDir      string
	ProvidersRoot string
	ManifestPath  string
	ResumePath    string
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
	Outputs   map[string][]string               `json:"outputs,omitempty"`
}

type resumeDoc struct {
	Block struct {
		Name string `json:"name"`
	} `json:"block"`
	Providers map[string]resumeProviderHint `json:"providers"`
}

type resumeProviderHint struct {
	Status          string   `json:"status"`
	Skipped         bool     `json:"skipped,omitempty"`
	CurrentStage    string   `json:"current_stage,omitempty"`
	CompletedStages []string `json:"completed_stages,omitempty"`
	StatePath       string   `json:"state_path,omitempty"`
	ProgressPath    string   `json:"progress_path,omitempty"`
	OutputPaths     []string `json:"output_paths,omitempty"`
	Error           string   `json:"error,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// Manifest is the exported type for reading manifest.json files.
// Downstream stages use this to resolve from_swarm inputs.
type Manifest struct {
	Block struct {
		Name string `json:"name"`
	} `json:"block"`
	Providers map[string]map[string]StageResult `json:"providers"`
	Outputs   map[string][]string               `json:"outputs,omitempty"`
}

// ResumeHints is the exported type for reading resume.json files.
// Used for crash recovery to determine which providers need re-execution.
type ResumeHints struct {
	Block struct {
		Name string `json:"name"`
	} `json:"block"`
	Providers map[string]ProviderHint `json:"providers"`
}

// ProviderHint captures one provider's recovery state.
type ProviderHint struct {
	Status          string   `json:"status"`
	Skipped         bool     `json:"skipped,omitempty"`
	CurrentStage    string   `json:"current_stage,omitempty"`
	CompletedStages []string `json:"completed_stages,omitempty"`
	StatePath       string   `json:"state_path,omitempty"`
	ProgressPath    string   `json:"progress_path,omitempty"`
	OutputPaths     []string `json:"output_paths,omitempty"`
	Error           string   `json:"error,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// ReadManifest reads and parses a manifest.json file.
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("swarm: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("swarm: parse manifest: %w", err)
	}
	if m.Providers == nil {
		m.Providers = map[string]map[string]StageResult{}
	}
	if m.Outputs == nil {
		m.Outputs = map[string][]string{}
	}
	return &m, nil
}

// ReadResumeHints reads and parses a resume.json file.
func ReadResumeHints(path string) (*ResumeHints, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("swarm: read resume: %w", err)
	}
	var h ResumeHints
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("swarm: parse resume: %w", err)
	}
	if h.Providers == nil {
		h.Providers = map[string]ProviderHint{}
	}
	return &h, nil
}

type providerWork struct {
	spec         compile.ProviderConfig
	name         string
	dir          string
	progressPath string
	statePath    string
}

// Run executes a swarm block.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if strings.TrimSpace(cfg.RunDir) == "" {
		return Result{}, fmt.Errorf("swarm: run dir is required")
	}
	if cfg.Executor == nil {
		return Result{}, fmt.Errorf("swarm: executor is required")
	}
	if len(cfg.Providers) == 0 {
		return Result{}, fmt.Errorf("swarm: at least one provider is required")
	}
	if len(cfg.Stages) == 0 {
		return Result{}, fmt.Errorf("swarm: at least one stage is required")
	}

	blockDirName := formatBlockDirName(cfg.BlockIndex, cfg.BlockID)
	blockDir := filepath.Join(cfg.RunDir, blockDirName)
	providersRoot := filepath.Join(blockDir, "providers")
	if err := os.MkdirAll(providersRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("swarm: create providers root: %w", err)
	}

	out := Result{
		BlockDir:      blockDir,
		ProvidersRoot: providersRoot,
		ManifestPath:  filepath.Join(blockDir, manifestFile),
		ResumePath:    filepath.Join(blockDir, resumeFile),
		Providers:     make(map[string]ProviderResult, len(cfg.Providers)),
	}

	previousStages := map[string]map[string]StageResult{}
	if cfg.Resume {
		manifest, err := readManifest(out.ManifestPath)
		if err == nil {
			previousStages = manifest.Providers
		}
	}

	// Auto-suffix duplicate provider names so each instance gets a unique directory.
	suffixedProviders := suffixDuplicateProviders(cfg.Providers)

	workItems := make([]providerWork, 0, len(suffixedProviders))
	for _, sp := range suffixedProviders {
		name := sp.instanceName
		if name == "" {
			return Result{}, fmt.Errorf("swarm: provider name is required")
		}

		providerDir := filepath.Join(providersRoot, name)
		progressPath := filepath.Join(providerDir, providerProgFile)
		statePath := filepath.Join(providerDir, providerStateFile)
		if err := os.MkdirAll(providerDir, 0o755); err != nil {
			return Result{}, fmt.Errorf("swarm: create provider dir: %w", err)
		}
		if err := ensureProgressFile(progressPath); err != nil {
			return Result{}, err
		}

		work := providerWork{
			spec:         sp.spec,
			name:         name,
			dir:          providerDir,
			progressPath: progressPath,
			statePath:    statePath,
		}
		if cfg.Resume {
			prev, err := readProviderState(statePath)
			if err == nil && strings.EqualFold(prev.Status, string(statusCompleted)) {
				stages := map[string]StageResult{}
				if existingStages, ok := previousStages[name]; ok {
					stages = copyStageMap(existingStages)
				}
				out.Providers[name] = ProviderResult{
					Name:         name,
					Directory:    providerDir,
					ProgressPath: progressPath,
					StatePath:    statePath,
					Status:       prev.Status,
					Skipped:      true,
					Stages:       stages,
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
	if err := writeResume(out.ResumePath, strings.TrimSpace(cfg.BlockID), out.Providers); err != nil {
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
	stages []compile.SwarmStage,
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
			return result, fmt.Errorf("swarm: create stage dir: %w", err)
		}
		// Use the instance name (e.g. "claude-2") in the request so the
		// executor can distinguish between duplicate provider instances.
		instanceSpec := work.spec
		instanceSpec.Name = work.name
		stageRes, err := executor.Execute(ctx, ExecuteRequest{
			BlockDir:      blockDir,
			Provider:      instanceSpec,
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
			return result, fmt.Errorf("swarm: provider %s stage %s: %w", work.name, stageKey, err)
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
		Outputs:   make(map[string][]string, len(providers)),
	}
	if strings.TrimSpace(blockID) == "" {
		manifest.Block.Name = "swarm"
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
		outputPaths := collectOutputPaths(providers[key].Stages)
		if len(outputPaths) > 0 {
			manifest.Outputs[key] = outputPaths
		}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("swarm: marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("swarm: write manifest: %w", err)
	}
	return nil
}

func readManifest(path string) (manifestBlock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifestBlock{}, err
	}
	var manifest manifestBlock
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifestBlock{}, err
	}
	if manifest.Providers == nil {
		manifest.Providers = map[string]map[string]StageResult{}
	}
	if manifest.Outputs == nil {
		manifest.Outputs = map[string][]string{}
	}
	return manifest, nil
}

func writeResume(path, blockID string, providers map[string]ProviderResult) error {
	doc := resumeDoc{
		Providers: make(map[string]resumeProviderHint, len(providers)),
	}
	if strings.TrimSpace(blockID) == "" {
		doc.Block.Name = "swarm"
	} else {
		doc.Block.Name = strings.TrimSpace(blockID)
	}

	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		result := providers[key]
		hint := resumeProviderHint{
			Status:       strings.TrimSpace(result.Status),
			Skipped:      result.Skipped,
			StatePath:    result.StatePath,
			ProgressPath: result.ProgressPath,
			OutputPaths:  collectOutputPaths(result.Stages),
			Error:        strings.TrimSpace(result.Error),
		}

		state, err := readProviderState(result.StatePath)
		if err == nil {
			if hint.Status == "" {
				hint.Status = strings.TrimSpace(state.Status)
			}
			hint.CurrentStage = strings.TrimSpace(state.CurrentStage)
			hint.CompletedStages = append([]string(nil), state.CompletedStages...)
			hint.UpdatedAt = strings.TrimSpace(state.UpdatedAt)
			if hint.Error == "" {
				hint.Error = strings.TrimSpace(state.Error)
			}
		}
		if hint.Status == "" {
			hint.Status = "unknown"
		}
		doc.Providers[key] = hint
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("swarm: marshal resume: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("swarm: write resume: %w", err)
	}
	return nil
}

func copyStageMap(values map[string]StageResult) map[string]StageResult {
	if len(values) == 0 {
		return map[string]StageResult{}
	}
	out := make(map[string]StageResult, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func collectOutputPaths(stages map[string]StageResult) []string {
	if len(stages) == 0 {
		return nil
	}
	stageKeys := make([]string, 0, len(stages))
	for stageKey := range stages {
		stageKeys = append(stageKeys, stageKey)
	}
	sort.Strings(stageKeys)

	outputs := make([]string, 0, len(stageKeys))
	seen := map[string]struct{}{}
	for _, stageKey := range stageKeys {
		path := strings.TrimSpace(stages[stageKey].LatestOutput)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		outputs = append(outputs, path)
	}
	return outputs
}

func ensureProgressFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		return fmt.Errorf("swarm: create progress file: %w", err)
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
		return fmt.Errorf("swarm: marshal provider state: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("swarm: write provider state: %w", err)
	}
	return nil
}

func effectiveStageRuns(stage compile.SwarmStage) int {
	if stage.Runs > 0 {
		return stage.Runs
	}
	return 1
}

func stageDirectoryName(index int, stage compile.SwarmStage) string {
	return fmt.Sprintf("stage-%02d-%s", index, sanitizeSegment(parallelStageKey(stage, index)))
}

func parallelStageKey(stage compile.SwarmStage, index int) string {
	return stage.Key(index)
}

func formatBlockDirName(index int, blockID string) string {
	if index < 0 {
		index = 0
	}
	base := fmt.Sprintf("swarm-%02d", index)
	if trimmed := strings.TrimSpace(blockID); trimmed != "" {
		return base + "-" + sanitizeSegment(trimmed)
	}
	return base
}

func normalizedProviderName(name string) string {
	return sanitizeSegment(strings.ToLower(strings.TrimSpace(name)))
}

type suffixedProvider struct {
	spec         compile.ProviderConfig
	instanceName string
}

// suffixDuplicateProviders detects duplicate normalized provider names and
// auto-suffixes them: [claude, claude, claude] → [claude-1, claude-2, claude-3].
// Single unique providers keep their original names: [claude, codex] → [claude, codex].
func suffixDuplicateProviders(providers []compile.ProviderConfig) []suffixedProvider {
	// Count occurrences of each normalized name.
	counts := map[string]int{}
	for _, p := range providers {
		counts[normalizedProviderName(p.Name)]++
	}

	// Track the next suffix index per name.
	nextIndex := map[string]int{}
	result := make([]suffixedProvider, 0, len(providers))
	for _, p := range providers {
		name := normalizedProviderName(p.Name)
		if counts[name] > 1 {
			idx := nextIndex[name] + 1
			nextIndex[name] = idx
			result = append(result, suffixedProvider{
				spec:         p,
				instanceName: fmt.Sprintf("%s-%d", name, idx),
			})
		} else {
			result = append(result, suffixedProvider{
				spec:         p,
				instanceName: name,
			})
		}
	}
	return result
}

// StripInstanceSuffix removes a trailing "-N" instance suffix from a provider
// name to recover the canonical provider type (e.g. "claude-2" → "claude").
// Names without a numeric suffix are returned as-is.
func StripInstanceSuffix(instanceName string) string {
	idx := strings.LastIndex(instanceName, "-")
	if idx < 0 {
		return instanceName
	}
	suffix := instanceName[idx+1:]
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return instanceName
		}
	}
	if len(suffix) == 0 {
		return instanceName
	}
	return instanceName[:idx]
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
