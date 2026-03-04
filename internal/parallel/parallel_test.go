package parallel

import (
	"context"
	"encoding/json"
	"errors"
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
		Stages: []compile.ParallelStage{
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

	if !strings.Contains(res.BlockDir, "parallel-01") {
		t.Fatalf("block dir %q missing parallel-01 prefix", res.BlockDir)
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
		Stages: []compile.ParallelStage{
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
		Stages: []compile.ParallelStage{
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
