package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
)

func TestRun_ConcurrentExecutionIsolationAndStructure(t *testing.T) {
	runDir := t.TempDir()

	var (
		mu           sync.Mutex
		active       int
		maxActive    int
		providerRuns = map[string][]string{}
	)

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		providerRuns[req.Provider.Name] = append(providerRuns[req.Provider.Name], req.Stage.Stage)
		mu.Unlock()

		defer func() {
			mu.Lock()
			active--
			mu.Unlock()
		}()

		time.Sleep(30 * time.Millisecond)
		latestOutput := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(latestOutput, []byte(req.Provider.Name+"-"+req.Stage.Stage), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput:      latestOutput,
			Iterations:        req.Stage.Runs,
			TerminationReason: "completed",
			History:           []string{latestOutput},
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "fanout",
		BlockIndex: 1,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
			{ID: "refine", Stage: "refine", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if maxActive < 2 {
		t.Fatalf("expected concurrent execution, max active providers = %d", maxActive)
	}

	wantOrder := []string{"analyze", "refine"}
	if got := providerRuns["claude"]; !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("claude stage order = %#v, want %#v", got, wantOrder)
	}
	if got := providerRuns["codex"]; !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("codex stage order = %#v, want %#v", got, wantOrder)
	}

	if !strings.Contains(res.BlockDir, "swarm-01") {
		t.Fatalf("block dir %q missing swarm-01 prefix", res.BlockDir)
	}
	for _, provider := range []string{"claude", "codex"} {
		providerDir := filepath.Join(res.ProvidersRoot, provider)
		if _, err := os.Stat(providerDir); err != nil {
			t.Fatalf("provider dir missing %q: %v", providerDir, err)
		}
		if _, err := os.Stat(filepath.Join(providerDir, providerProgFile)); err != nil {
			t.Fatalf("progress file missing for %s: %v", provider, err)
		}
		statePath := filepath.Join(providerDir, providerStateFile)
		state, err := readProviderState(statePath)
		if err != nil {
			t.Fatalf("read provider state %s: %v", provider, err)
		}
		if state.Status != string(statusCompleted) {
			t.Fatalf("provider %s status = %q, want completed", provider, state.Status)
		}
	}
	if _, err := os.Stat(res.ManifestPath); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	manifest, err := readManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if _, ok := manifest.Providers["claude"]["analyze"]; !ok {
		t.Fatalf("manifest missing stage result for downstream reads: %#v", manifest.Providers["claude"])
	}
	for _, provider := range []string{"claude", "codex"} {
		outputs := manifest.Outputs[provider]
		if len(outputs) != 2 {
			t.Fatalf("manifest outputs for %s = %#v, want 2 outputs", provider, outputs)
		}
	}

	if _, err := os.Stat(res.ResumePath); err != nil {
		t.Fatalf("resume file missing: %v", err)
	}
	resume := readResumeDoc(t, res.ResumePath)
	for _, provider := range []string{"claude", "codex"} {
		hint, ok := resume.Providers[provider]
		if !ok {
			t.Fatalf("resume missing provider %q", provider)
		}
		if hint.Status != string(statusCompleted) {
			t.Fatalf("resume status for %s = %q, want completed", provider, hint.Status)
		}
		if len(hint.OutputPaths) != 2 {
			t.Fatalf("resume output_paths for %s = %#v, want 2 outputs", provider, hint.OutputPaths)
		}
	}
}

func TestRun_FailurePropagatesAfterAllProvidersFinish(t *testing.T) {
	runDir := t.TempDir()

	var (
		mu             sync.Mutex
		completedCount int
	)

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		if req.Provider.Name == "codex" {
			return StageResult{}, errors.New("codex failed")
		}
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		completedCount++
		mu.Unlock()
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "fail",
		BlockIndex: 2,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	if err == nil {
		t.Fatal("expected provider failure to fail the block")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Fatalf("error %q should include failing provider", err.Error())
	}

	mu.Lock()
	gotCompleted := completedCount
	mu.Unlock()
	if gotCompleted != 1 {
		t.Fatalf("expected successful provider to finish before return, completed count = %d", gotCompleted)
	}
	if res.Providers["codex"].Status != string(statusFailed) {
		t.Fatalf("codex status = %q, want failed", res.Providers["codex"].Status)
	}
	if res.Providers["claude"].Status != string(statusCompleted) {
		t.Fatalf("claude status = %q, want completed", res.Providers["claude"].Status)
	}

	manifest, manErr := readManifest(res.ManifestPath)
	if manErr != nil {
		t.Fatalf("read manifest: %v", manErr)
	}
	if got := manifest.Outputs["claude"]; len(got) != 1 {
		t.Fatalf("manifest outputs for claude = %#v, want 1 output", got)
	}
	if got := manifest.Outputs["codex"]; len(got) != 0 {
		t.Fatalf("manifest outputs for codex = %#v, want no outputs", got)
	}

	resume := readResumeDoc(t, res.ResumePath)
	if resume.Providers["claude"].Status != string(statusCompleted) {
		t.Fatalf("resume claude status = %q, want completed", resume.Providers["claude"].Status)
	}
	if resume.Providers["codex"].Status != string(statusFailed) {
		t.Fatalf("resume codex status = %q, want failed", resume.Providers["codex"].Status)
	}
	if !strings.Contains(resume.Providers["codex"].Error, "codex failed") {
		t.Fatalf("resume codex error = %q, want codex failed", resume.Providers["codex"].Error)
	}
}

func TestRun_ResumeSkipsCompletedProviders(t *testing.T) {
	runDir := t.TempDir()
	blockDir := filepath.Join(runDir, formatBlockDirName(3, "resume"))
	claudeDir := filepath.Join(blockDir, "providers", "claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := ensureProgressFile(filepath.Join(claudeDir, providerProgFile)); err != nil {
		t.Fatalf("create claude progress: %v", err)
	}
	if err := writeProviderState(filepath.Join(claudeDir, providerStateFile), providerState{
		Provider:  "claude",
		Status:    string(statusCompleted),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed claude state: %v", err)
	}
	existingOutput := filepath.Join(claudeDir, "stage-01-analyze", "output.md")
	if err := os.MkdirAll(filepath.Dir(existingOutput), 0o755); err != nil {
		t.Fatalf("mkdir existing output dir: %v", err)
	}
	if err := os.WriteFile(existingOutput, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	if err := writeManifest(filepath.Join(blockDir, manifestFile), "resume", map[string]ProviderResult{
		"claude": {
			Name: "claude",
			Stages: map[string]StageResult{
				"analyze": {
					LatestOutput: existingOutput,
					Status:       string(statusCompleted),
					Iterations:   1,
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	var (
		mu           sync.Mutex
		providerRuns []string
	)

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		mu.Lock()
		providerRuns = append(providerRuns, req.Provider.Name)
		mu.Unlock()
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "resume",
		BlockIndex: 3,
		Resume:     true,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if !res.Providers["claude"].Skipped {
		t.Fatal("expected completed claude provider to be skipped on resume")
	}
	if res.Providers["codex"].Skipped {
		t.Fatal("expected codex provider to run on resume")
	}

	mu.Lock()
	gotRuns := append([]string(nil), providerRuns...)
	mu.Unlock()
	if !reflect.DeepEqual(gotRuns, []string{"codex"}) {
		t.Fatalf("provider runs = %#v, want [codex]", gotRuns)
	}

	manifest, manErr := readManifest(res.ManifestPath)
	if manErr != nil {
		t.Fatalf("read manifest: %v", manErr)
	}
	if _, ok := manifest.Providers["claude"]["analyze"]; !ok {
		t.Fatalf("expected resumed manifest to retain skipped provider stage data: %#v", manifest.Providers["claude"])
	}
	if got := manifest.Outputs["claude"]; !reflect.DeepEqual(got, []string{existingOutput}) {
		t.Fatalf("manifest outputs for skipped provider = %#v, want [%q]", got, existingOutput)
	}

	resume := readResumeDoc(t, res.ResumePath)
	if !resume.Providers["claude"].Skipped {
		t.Fatal("expected skipped provider hint in resume.json")
	}
	if got := resume.Providers["claude"].OutputPaths; !reflect.DeepEqual(got, []string{existingOutput}) {
		t.Fatalf("resume output_paths for skipped provider = %#v, want [%q]", got, existingOutput)
	}
}

func TestRun_DuplicateProviderNames_AutoSuffix(t *testing.T) {
	runDir := t.TempDir()

	var (
		mu             sync.Mutex
		providerNames  []string
		providerDirs   []string
	)

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		mu.Lock()
		providerNames = append(providerNames, req.Provider.Name)
		providerDirs = append(providerDirs, req.ProviderDir)
		mu.Unlock()
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "multi-claude",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Should have 3 providers with suffixed names.
	if len(res.Providers) != 3 {
		t.Fatalf("providers count = %d, want 3", len(res.Providers))
	}

	// Check that all three have unique names.
	expectedNames := map[string]bool{"claude-1": true, "claude-2": true, "claude-3": true}
	for name := range res.Providers {
		if !expectedNames[name] {
			t.Errorf("unexpected provider name %q", name)
		}
		delete(expectedNames, name)
	}
	if len(expectedNames) > 0 {
		t.Fatalf("missing provider names: %v", expectedNames)
	}

	// Each provider should have its own directory.
	for name, prov := range res.Providers {
		if !strings.Contains(prov.Directory, name) {
			t.Errorf("provider %q directory %q should contain its name", name, prov.Directory)
		}
	}
}

func TestRun_MixedProviders_NoSuffix(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "mixed",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Unique providers should keep their original names (no suffix).
	if _, ok := res.Providers["claude"]; !ok {
		t.Fatal("expected provider named 'claude'")
	}
	if _, ok := res.Providers["codex"]; !ok {
		t.Fatal("expected provider named 'codex'")
	}
}

func TestStripInstanceSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"claude-1", "claude"},
		{"claude-2", "claude"},
		{"claude-10", "claude"},
		{"claude", "claude"},
		{"codex", "codex"},
		{"claude-code", "claude-code"},    // "code" is not numeric
		{"my-provider-3", "my-provider"},
	}

	for _, tt := range tests {
		got := StripInstanceSuffix(tt.input)
		if got != tt.want {
			t.Errorf("StripInstanceSuffix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestRun_FiveProviders_AllOutputsInManifest verifies that running 5 duplicate
// providers through multiple stages produces a manifest with every output path
// preserved and readable — nothing lost to concurrency.
func TestRun_FiveProviders_AllOutputsInManifest(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		// Write distinct content per provider+stage so we can verify later.
		content := fmt.Sprintf("output from %s stage %s", req.Provider.Name, req.Stage.Stage)
		outputPath := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput:      outputPath,
			Iterations:        req.Stage.Runs,
			TerminationReason: "fixed",
			History:           []string{outputPath},
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "five-way",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
		},
		Stages: []compile.SwarmStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
			{ID: "refine", Stage: "refine", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// All 5 providers must appear.
	if len(res.Providers) != 5 {
		t.Fatalf("providers count = %d, want 5", len(res.Providers))
	}

	// Read manifest and verify every provider has both stages with readable output.
	manifest, err := readManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("claude-%d", i)
		provStages, ok := manifest.Providers[name]
		if !ok {
			t.Fatalf("manifest missing provider %q; got keys: %v", name, mapKeys(manifest.Providers))
		}
		for _, stageID := range []string{"analyze", "refine"} {
			sr, ok := provStages[stageID]
			if !ok {
				t.Fatalf("manifest[%s] missing stage %q", name, stageID)
			}
			// Verify the output file exists and has correct content.
			data, err := os.ReadFile(sr.LatestOutput)
			if err != nil {
				t.Fatalf("read output for %s/%s: %v", name, stageID, err)
			}
			wantContent := fmt.Sprintf("output from %s stage %s", name, stageID)
			if string(data) != wantContent {
				t.Fatalf("output for %s/%s = %q, want %q", name, stageID, string(data), wantContent)
			}
		}

		// Verify the outputs list has 2 entries.
		outputs := manifest.Outputs[name]
		if len(outputs) != 2 {
			t.Fatalf("manifest outputs for %s = %d, want 2", name, len(outputs))
		}
	}
}

// TestRun_PartialFailure_SuccessfulOutputsPreserved verifies that when one of
// multiple providers fails, the successful providers' output files are still
// fully intact and readable from the manifest.
func TestRun_PartialFailure_SuccessfulOutputsPreserved(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		if req.Provider.Name == "claude-3" {
			return StageResult{}, errors.New("claude-3 crashed")
		}
		content := fmt.Sprintf("good output from %s", req.Provider.Name)
		outputPath := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return StageResult{}, err
		}
		time.Sleep(20 * time.Millisecond) // let failed provider finish first
		return StageResult{
			LatestOutput:      outputPath,
			Iterations:        1,
			TerminationReason: "fixed",
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "partial-fail",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
		},
		Stages: []compile.SwarmStage{
			{ID: "review", Stage: "review", Runs: 1},
		},
		Executor: executor,
	})
	if err == nil {
		t.Fatal("expected error from partial failure")
	}

	// Verify failed provider is marked failed.
	if res.Providers["claude-3"].Status != string(statusFailed) {
		t.Fatalf("claude-3 status = %q, want failed", res.Providers["claude-3"].Status)
	}

	// Verify successful providers completed.
	for _, name := range []string{"claude-1", "claude-2"} {
		if res.Providers[name].Status != string(statusCompleted) {
			t.Fatalf("%s status = %q, want completed", name, res.Providers[name].Status)
		}
	}

	// Verify manifest preserves successful outputs with correct content.
	manifest, err := readManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, name := range []string{"claude-1", "claude-2"} {
		sr, ok := manifest.Providers[name]["review"]
		if !ok {
			t.Fatalf("manifest missing %s/review", name)
		}
		data, err := os.ReadFile(sr.LatestOutput)
		if err != nil {
			t.Fatalf("read output for %s: %v", name, err)
		}
		want := fmt.Sprintf("good output from %s", name)
		if string(data) != want {
			t.Fatalf("output content for %s = %q, want %q", name, string(data), want)
		}
	}

	// Failed provider should have no outputs.
	if outputs := manifest.Outputs["claude-3"]; len(outputs) != 0 {
		t.Fatalf("failed provider should have 0 outputs, got %d", len(outputs))
	}
}

// TestRun_ContextCancellation_AllProvidersStop verifies that cancelling the
// context propagates to all running providers and they stop cleanly.
func TestRun_ContextCancellation_AllProvidersStop(t *testing.T) {
	runDir := t.TempDir()

	var (
		mu       sync.Mutex
		started  int
		finished int
	)

	executor := ExecutorFunc(func(ctx context.Context, req ExecuteRequest) (StageResult, error) {
		mu.Lock()
		started++
		mu.Unlock()

		// Block until context is cancelled.
		<-ctx.Done()

		mu.Lock()
		finished++
		mu.Unlock()

		return StageResult{}, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay to let providers start.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := Run(ctx, Config{
		RunDir:     runDir,
		BlockID:    "cancel-test",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
			{Name: "gemini"},
		},
		Stages: []compile.SwarmStage{
			{ID: "work", Stage: "work", Runs: 1},
		},
		Executor: executor,
	})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}

	mu.Lock()
	gotStarted := started
	gotFinished := finished
	mu.Unlock()

	if gotStarted != 3 {
		t.Fatalf("started = %d, want 3 (all providers should have started)", gotStarted)
	}
	if gotFinished != 3 {
		t.Fatalf("finished = %d, want 3 (all providers should have received cancellation)", gotFinished)
	}
}

// TestRun_ManifestContentRoundTrip verifies that output paths written to
// manifest.json are valid, readable, and contain the expected content when
// parsed back using readManifest().
func TestRun_ManifestContentRoundTrip(t *testing.T) {
	runDir := t.TempDir()

	// Each provider writes unique, identifiable content.
	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		content := fmt.Sprintf("CONTENT[%s/%s]", req.Provider.Name, req.Stage.ID)
		outputPath := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput:      outputPath,
			Iterations:        1,
			TerminationReason: "fixed",
			History:           []string{outputPath},
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "roundtrip",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.SwarmStage{
			{ID: "draft", Stage: "draft", Runs: 1},
			{ID: "polish", Stage: "polish", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Read manifest back.
	manifest, err := readManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// Verify every output path in the manifest is readable with correct content.
	for provName, stages := range manifest.Providers {
		for stageID, sr := range stages {
			if sr.LatestOutput == "" {
				t.Fatalf("manifest[%s][%s] has empty latest_output", provName, stageID)
			}
			data, err := os.ReadFile(sr.LatestOutput)
			if err != nil {
				t.Fatalf("read manifest[%s][%s] output at %q: %v", provName, stageID, sr.LatestOutput, err)
			}
			want := fmt.Sprintf("CONTENT[%s/%s]", provName, stageID)
			if string(data) != want {
				t.Fatalf("manifest[%s][%s] content = %q, want %q", provName, stageID, string(data), want)
			}
		}
	}

	// Verify the outputs list matches the stage results.
	for provName, outputPaths := range manifest.Outputs {
		if len(outputPaths) != 2 {
			t.Fatalf("manifest outputs[%s] count = %d, want 2", provName, len(outputPaths))
		}
		for _, p := range outputPaths {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("manifest output path %q not readable: %v", p, err)
			}
		}
	}

	// Also verify resume.json output paths are consistent.
	resume := readResumeDoc(t, res.ResumePath)
	for provName, hint := range resume.Providers {
		if len(hint.OutputPaths) != 2 {
			t.Fatalf("resume[%s] output_paths count = %d, want 2", provName, len(hint.OutputPaths))
		}
		for _, p := range hint.OutputPaths {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("resume output path %q not readable: %v", p, err)
			}
		}
	}
}

// TestRun_DuplicateProviders_MultiStage_AllStagesRecorded verifies that
// duplicate providers running multiple stages have ALL stage results recorded
// in the manifest — no overwrites, no missing entries.
func TestRun_DuplicateProviders_MultiStage_AllStagesRecorded(t *testing.T) {
	runDir := t.TempDir()

	var (
		mu        sync.Mutex
		callCount int
	)

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		mu.Lock()
		callCount++
		mu.Unlock()

		outputPath := filepath.Join(req.StageDir, "output.md")
		content := fmt.Sprintf("%s/%s", req.Provider.Name, req.Stage.ID)
		if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput:      outputPath,
			Iterations:        req.Stage.Runs,
			TerminationReason: "fixed",
			History:           []string{outputPath},
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "multi-stage-dup",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "claude"},
			{Name: "claude"},
		},
		Stages: []compile.SwarmStage{
			{ID: "plan", Stage: "plan", Runs: 1},
			{ID: "implement", Stage: "implement", Runs: 1},
			{ID: "test", Stage: "test", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mu.Lock()
	gotCalls := callCount
	mu.Unlock()

	// 3 providers x 3 stages = 9 executor calls.
	if gotCalls != 9 {
		t.Fatalf("executor calls = %d, want 9 (3 providers x 3 stages)", gotCalls)
	}

	// Verify manifest has all 9 stage results.
	manifest, err := readManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("claude-%d", i)
		provStages, ok := manifest.Providers[name]
		if !ok {
			t.Fatalf("manifest missing provider %q", name)
		}
		if len(provStages) != 3 {
			t.Fatalf("manifest[%s] stage count = %d, want 3", name, len(provStages))
		}
		for _, stageID := range []string{"plan", "implement", "test"} {
			sr, ok := provStages[stageID]
			if !ok {
				t.Fatalf("manifest[%s] missing stage %q", name, stageID)
			}
			// Verify content.
			data, err := os.ReadFile(sr.LatestOutput)
			if err != nil {
				t.Fatalf("read %s/%s output: %v", name, stageID, err)
			}
			if string(data) != fmt.Sprintf("%s/%s", name, stageID) {
				t.Fatalf("content = %q, want %q", string(data), fmt.Sprintf("%s/%s", name, stageID))
			}
		}

		// Verify outputs list has 3 entries.
		if len(manifest.Outputs[name]) != 3 {
			t.Fatalf("manifest outputs[%s] = %d, want 3", name, len(manifest.Outputs[name]))
		}
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func readResumeDoc(t *testing.T, path string) resumeDoc {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc resumeDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}
