package parallel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/compile"
)

func TestManifest_MapsProviderNamesToOutputPaths(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		outputPath := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(outputPath, []byte(req.Provider.Name+"-output"), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput: outputPath,
			Status:       "completed",
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "manifest-test",
		BlockIndex: 0,
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

	// Read manifest back.
	manifest, err := ReadManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}

	if manifest.Block.Name != "manifest-test" {
		t.Fatalf("block name = %q, want manifest-test", manifest.Block.Name)
	}

	// Both providers should be in manifest with output paths.
	for _, prov := range []string{"claude", "codex"} {
		stages, ok := manifest.Providers[prov]
		if !ok {
			t.Fatalf("provider %q missing from manifest", prov)
		}
		stageResult, ok := stages["analyze"]
		if !ok {
			t.Fatalf("provider %q stage analyze missing from manifest", prov)
		}
		if stageResult.LatestOutput == "" {
			t.Fatalf("provider %q analyze.latest_output is empty", prov)
		}
		// Verify the output file actually exists.
		if _, err := os.Stat(stageResult.LatestOutput); err != nil {
			t.Fatalf("output file for %q doesn't exist: %v", prov, err)
		}
	}
}

func TestReadManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	providers := map[string]ProviderResult{
		"claude": {
			Name:   "claude",
			Status: "completed",
			Stages: map[string]StageResult{
				"plan": {
					LatestOutput:      "/tmp/plan/output.md",
					Status:            "completed",
					Iterations:        3,
					TerminationReason: "fixed",
					History:           []string{"/tmp/plan/001.md", "/tmp/plan/002.md", "/tmp/plan/003.md"},
				},
			},
		},
	}

	if err := writeManifest(path, "round-trip", providers); err != nil {
		t.Fatalf("writeManifest() error: %v", err)
	}

	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}
	if manifest.Block.Name != "round-trip" {
		t.Fatalf("block name = %q, want round-trip", manifest.Block.Name)
	}
	stageRes := manifest.Providers["claude"]["plan"]
	if stageRes.LatestOutput != "/tmp/plan/output.md" {
		t.Fatalf("latest_output = %q, want /tmp/plan/output.md", stageRes.LatestOutput)
	}
	if stageRes.Iterations != 3 {
		t.Fatalf("iterations = %d, want 3", stageRes.Iterations)
	}
	if len(stageRes.History) != 3 {
		t.Fatalf("history len = %d, want 3", len(stageRes.History))
	}
}

func TestReadManifest_MissingFile(t *testing.T) {
	_, err := ReadManifest("/nonexistent/manifest.json")
	if err == nil {
		t.Fatal("expected error for missing manifest file")
	}
}

func TestResumeHints_GeneratedAfterRun(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Status:       "completed",
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "resume-gen",
		BlockIndex: 0,
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

	// resume.json should exist at block level.
	resumePath := filepath.Join(res.BlockDir, resumeFile)
	hints, err := ReadResumeHints(resumePath)
	if err != nil {
		t.Fatalf("ReadResumeHints() error: %v", err)
	}

	if len(hints.Providers) != 2 {
		t.Fatalf("expected 2 provider hints, got %d", len(hints.Providers))
	}

	for _, prov := range []string{"claude", "codex"} {
		hint, ok := hints.Providers[prov]
		if !ok {
			t.Fatalf("provider %q missing from resume hints", prov)
		}
		if hint.Status != "completed" {
			t.Fatalf("provider %q status = %q, want completed", prov, hint.Status)
		}
		if len(hint.CompletedStages) != 1 {
			t.Fatalf("provider %q completed_stages len = %d, want 1", prov, len(hint.CompletedStages))
		}
	}
}

func TestResumeHints_PartialCompletion(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		if req.Provider.Name == "codex" {
			return StageResult{}, os.ErrPermission
		}
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Status:       "completed",
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "partial",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
			{Name: "codex"},
		},
		Stages: []compile.ParallelStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	// Should get an error because codex failed.
	if err == nil {
		t.Fatal("expected error for partial completion")
	}

	// resume.json should still exist with both providers' status.
	resumePath := filepath.Join(res.BlockDir, resumeFile)
	hints, err := ReadResumeHints(resumePath)
	if err != nil {
		t.Fatalf("ReadResumeHints() error: %v", err)
	}

	claudeHint := hints.Providers["claude"]
	if claudeHint.Status != "completed" {
		t.Fatalf("claude status = %q, want completed", claudeHint.Status)
	}
	codexHint := hints.Providers["codex"]
	if codexHint.Status != "failed" {
		t.Fatalf("codex status = %q, want failed", codexHint.Status)
	}
	if codexHint.Error == "" {
		t.Fatal("codex error should be non-empty")
	}
}

func TestResumeHints_ReadMissingFile(t *testing.T) {
	_, err := ReadResumeHints("/nonexistent/resume.json")
	if err == nil {
		t.Fatal("expected error for missing resume file")
	}
}

func TestManifest_MultipleStages(t *testing.T) {
	runDir := t.TempDir()

	callCount := 0
	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		callCount++
		outputPath := filepath.Join(req.StageDir, "output.md")
		if err := os.WriteFile(outputPath, []byte("output"), 0o644); err != nil {
			return StageResult{}, err
		}
		return StageResult{
			LatestOutput: outputPath,
			Status:       "completed",
			Iterations:   req.Stage.Runs,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "multi-stage",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
		},
		Stages: []compile.ParallelStage{
			{ID: "plan", Stage: "plan", Runs: 3},
			{ID: "refine", Stage: "refine", Runs: 2},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	manifest, err := ReadManifest(res.ManifestPath)
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}

	claudeStages := manifest.Providers["claude"]
	if len(claudeStages) != 2 {
		t.Fatalf("claude stages count = %d, want 2", len(claudeStages))
	}

	planResult, ok := claudeStages["plan"]
	if !ok {
		t.Fatal("plan stage missing from manifest")
	}
	if planResult.Iterations != 3 {
		t.Fatalf("plan iterations = %d, want 3", planResult.Iterations)
	}

	refineResult, ok := claudeStages["refine"]
	if !ok {
		t.Fatal("refine stage missing from manifest")
	}
	if refineResult.Iterations != 2 {
		t.Fatalf("refine iterations = %d, want 2", refineResult.Iterations)
	}
}

func TestResult_IncludesResumePath(t *testing.T) {
	runDir := t.TempDir()

	executor := ExecutorFunc(func(_ context.Context, req ExecuteRequest) (StageResult, error) {
		return StageResult{
			LatestOutput: filepath.Join(req.StageDir, "output.md"),
			Status:       "completed",
			Iterations:   1,
		}, nil
	})

	res, err := Run(context.Background(), Config{
		RunDir:     runDir,
		BlockID:    "result-resume",
		BlockIndex: 0,
		Providers: []compile.ProviderConfig{
			{Name: "claude"},
		},
		Stages: []compile.ParallelStage{
			{ID: "analyze", Stage: "analyze", Runs: 1},
		},
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.ResumePath == "" {
		t.Fatal("Result.ResumePath should be set")
	}
	// File should exist.
	if _, err := os.Stat(res.ResumePath); err != nil {
		t.Fatalf("resume.json doesn't exist at %q: %v", res.ResumePath, err)
	}

	// Verify it's valid JSON.
	data, err := os.ReadFile(res.ResumePath)
	if err != nil {
		t.Fatalf("read resume.json: %v", err)
	}
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("resume.json is not valid JSON: %v", err)
	}
}
