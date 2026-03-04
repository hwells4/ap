package parallel

import (
	"context"
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
}
