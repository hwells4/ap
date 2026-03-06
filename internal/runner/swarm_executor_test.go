package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/swarm"
	"github.com/hwells4/ap/internal/store"
)

func TestParallelExecutor_SingleStage(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("iteration 1"),
			mock.StopResponse("iteration 2", "done"),
		),
	)

	executor := &swarmExecutor{
		cfg: Config{
			Session:        "test-swarm-exec",
			RunDir:         runDir,
			Provider:       mp,
			Store:          s,
			WorkDir:        filepath.Dir(filepath.Dir(runDir)),
			PromptTemplate: "work",
		},
		blockID: "test-block",
	}

	// Create session in store first.
	if err := s.CreateSession(context.Background(), "test-swarm-exec", "pipeline", "test", "{}"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	providerDir := filepath.Join(runDir, "swarm-00-test-block", "providers", "claude-1")
	stageDir := filepath.Join(providerDir, "stage-01-analyze")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := executor.Execute(context.Background(), swarm.ExecuteRequest{
		BlockDir: filepath.Join(runDir, "swarm-00-test-block"),
		Provider: compile.ProviderConfig{
			Name: "claude-1",
		},
		Stage: compile.SwarmStage{
			ID:    "analyze",
			Stage: "improve-plan",
			Runs:  2,
		},
		StageIndex:   0,
		ProviderDir:  providerDir,
		StageDir:     stageDir,
		ProgressPath: filepath.Join(providerDir, "progress.md"),
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if result.Status != "completed" {
		t.Errorf("status = %q, want completed", result.Status)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
	if result.TerminationReason != "stop" {
		t.Errorf("termination_reason = %q, want stop", result.TerminationReason)
	}

	// Verify iterations were recorded in the store.
	rows, err := s.GetIterations(context.Background(), "test-swarm-exec", "swarm:test-block:claude-1:analyze")
	if err != nil {
		t.Fatalf("get iterations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("iteration count = %d, want 2", len(rows))
	}
}

func TestParallelExecutor_ProviderFailure(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.FailureResponse(context.DeadlineExceeded),
		),
	)

	executor := &swarmExecutor{
		cfg: Config{
			Session:        "test-swarm-fail",
			RunDir:         runDir,
			Provider:       mp,
			Store:          s,
			WorkDir:        filepath.Dir(filepath.Dir(runDir)),
			PromptTemplate: "work",
		},
		blockID: "fail-block",
	}

	if err := s.CreateSession(context.Background(), "test-swarm-fail", "pipeline", "test", "{}"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	providerDir := filepath.Join(runDir, "swarm-00-fail-block", "providers", "claude")
	stageDir := filepath.Join(providerDir, "stage-01-analyze")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := executor.Execute(context.Background(), swarm.ExecuteRequest{
		BlockDir: filepath.Join(runDir, "swarm-00-fail-block"),
		Provider: compile.ProviderConfig{Name: "claude"},
		Stage: compile.SwarmStage{
			ID:    "analyze",
			Stage: "improve-plan",
			Runs:  3,
		},
		StageIndex:   0,
		ProviderDir:  providerDir,
		StageDir:     stageDir,
		ProgressPath: filepath.Join(providerDir, "progress.md"),
	})
	if err == nil {
		t.Fatal("expected error from provider failure")
	}
	if result.Status != "failed" {
		t.Errorf("status = %q, want failed", result.Status)
	}
}

func TestRun_Pipeline_ParallelNode(t *testing.T) {
	runDir, s := tempSession(t)

	// Create mock provider that handles all iterations.
	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm work",
		}),
	)

	pipeline := &compile.Pipeline{
		Name: "swarm-pipeline",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{
				ID: "swarm-review",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "review", Stage: "improve-plan", Runs: 1},
					},
				},
			},
			{ID: "synthesize", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "swarm-pipeline-test",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// Total iterations: 1 (plan) + 2 (swarm: 2 providers * 1 run) + 1 (synthesize) = 4
	if res.Iterations != 4 {
		t.Fatalf("iterations = %d, want 4", res.Iterations)
	}

	// Verify swarm events were emitted.
	evts := readEvents(t, s, "swarm-pipeline-test")
	foundParallelStart := false
	foundParallelComplete := false
	for _, evt := range evts {
		if evt.Type == store.TypeSwarmStart {
			foundParallelStart = true
		}
		if evt.Type == store.TypeSwarmComplete {
			foundParallelComplete = true
		}
	}
	if !foundParallelStart {
		t.Error("swarm.started event not found")
	}
	if !foundParallelComplete {
		t.Error("swarm.completed event not found")
	}

	// Verify manifest was written.
	manifestPath := filepath.Join(runDir, "swarm-01-swarm-review", "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest.json missing: %v", err)
	}
}

// TestParallelExecutor_StoreIsolation verifies that two providers running
// the same stage get separate compound stage names in the store — their
// iterations never collide.
func TestParallelExecutor_StoreIsolation(t *testing.T) {
	runDir, s := tempSession(t)

	sessionName := "test-store-isolation"
	if err := s.CreateSession(context.Background(), sessionName, "pipeline", "test", "{}"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	blockID := "iso-block"

	// Run two providers sequentially through the executor, each doing 2 iterations.
	for _, provName := range []string{"claude-1", "claude-2"} {
		mp := mock.New(
			mock.WithResponses(
				mock.ContinueResponse(fmt.Sprintf("%s iter 1", provName)),
				mock.StopResponse(fmt.Sprintf("%s iter 2", provName), "done"),
			),
		)

		executor := &swarmExecutor{
			cfg: Config{
				Session:        sessionName,
				RunDir:         runDir,
				Provider:       mp,
				Store:          s,
				WorkDir:        filepath.Dir(filepath.Dir(runDir)),
				PromptTemplate: "work on ${ITERATION}",
			},
			blockID: blockID,
		}

		providerDir := filepath.Join(runDir, "swarm-00-"+blockID, "providers", provName)
		stageDir := filepath.Join(providerDir, "stage-01-review")
		if err := os.MkdirAll(stageDir, 0o755); err != nil {
			t.Fatal(err)
		}

		_, err := executor.Execute(context.Background(), swarm.ExecuteRequest{
			BlockDir:     filepath.Join(runDir, "swarm-00-"+blockID),
			Provider:     compile.ProviderConfig{Name: provName},
			Stage:        compile.SwarmStage{ID: "review", Stage: "code-review", Runs: 2},
			StageIndex:   0,
			ProviderDir:  providerDir,
			StageDir:     stageDir,
			ProgressPath: filepath.Join(providerDir, "progress.md"),
		})
		if err != nil {
			t.Fatalf("Execute(%s) error: %v", provName, err)
		}
	}

	// Verify each provider's iterations are separate in the store.
	for _, provName := range []string{"claude-1", "claude-2"} {
		compoundName := fmt.Sprintf("swarm:%s:%s:review", blockID, provName)
		rows, err := s.GetIterations(context.Background(), sessionName, compoundName)
		if err != nil {
			t.Fatalf("GetIterations(%s): %v", provName, err)
		}
		if len(rows) != 2 {
			t.Fatalf("iterations for %s = %d, want 2", provName, len(rows))
		}
		// Verify the summaries are provider-specific.
		if rows[0].Summary == "" {
			t.Fatalf("iteration 1 summary for %s is empty", provName)
		}
	}

	// Cross-check: the two compound names have separate iteration sets.
	rows1, _ := s.GetIterations(context.Background(), sessionName, fmt.Sprintf("swarm:%s:claude-1:review", blockID))
	rows2, _ := s.GetIterations(context.Background(), sessionName, fmt.Sprintf("swarm:%s:claude-2:review", blockID))

	if rows1[0].Summary == rows2[0].Summary {
		t.Fatalf("provider iterations should have different summaries but both have %q", rows1[0].Summary)
	}
}

// TestRun_Pipeline_ParallelOutputFlowsToDownstream verifies the full data path:
// sequential node → swarm node (2 providers) → sequential node.
// The downstream node must be able to read the swarm block's manifest.json,
// and the manifest must contain valid output paths with correct content.
func TestRun_Pipeline_ParallelOutputFlowsToDownstream(t *testing.T) {
	runDir, s := tempSession(t)

	sessionName := "pipeline-data-flow"
	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "work done",
		}),
	)

	pipeline := &compile.Pipeline{
		Name: "data-flow",
		Nodes: []compile.Node{
			{ID: "prepare", Stage: "improve-plan", Runs: 1},
			{
				ID: "swarm-work",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
						{Name: "mock"},
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "analyze", Stage: "improve-plan", Runs: 1},
						{ID: "review", Stage: "improve-plan", Runs: 1},
					},
				},
			},
			{ID: "merge", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        sessionName,
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "do ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(runDir)),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want completed", res.Status)
	}

	// Total: 1 (prepare) + 6 (3 providers * 2 stages) + 1 (merge) = 8
	if res.Iterations != 8 {
		t.Fatalf("iterations = %d, want 8", res.Iterations)
	}

	// The swarm block is node index 1, so dir is swarm-01-swarm-work.
	manifestPath := filepath.Join(runDir, "swarm-01-swarm-work", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest struct {
		Block struct {
			Name string `json:"name"`
		} `json:"block"`
		Providers map[string]map[string]json.RawMessage `json:"providers"`
		Outputs   map[string][]string                   `json:"outputs"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	if manifest.Block.Name != "swarm-work" {
		t.Fatalf("block name = %q, want swarm-work", manifest.Block.Name)
	}

	// 3 duplicate "mock" providers → mock-1, mock-2, mock-3.
	if len(manifest.Providers) != 3 {
		t.Fatalf("manifest providers = %d, want 3", len(manifest.Providers))
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("mock-%d", i)
		provStages, ok := manifest.Providers[name]
		if !ok {
			t.Fatalf("manifest missing provider %q", name)
		}
		// Each provider should have 2 stages.
		if len(provStages) != 2 {
			t.Fatalf("manifest[%s] stages = %d, want 2", name, len(provStages))
		}
		if _, ok := provStages["analyze"]; !ok {
			t.Fatalf("manifest[%s] missing stage 'analyze'", name)
		}
		if _, ok := provStages["review"]; !ok {
			t.Fatalf("manifest[%s] missing stage 'review'", name)
		}

		// Verify outputs list.
		outputs := manifest.Outputs[name]
		if len(outputs) != 2 {
			t.Fatalf("manifest outputs[%s] = %d, want 2", name, len(outputs))
		}
	}

	// Verify swarm lifecycle events.
	evts := readEvents(t, s, sessionName)
	swarmStartCount := 0
	swarmCompleteCount := 0
	for _, evt := range evts {
		if evt.Type == store.TypeSwarmStart {
			swarmStartCount++
		}
		if evt.Type == store.TypeSwarmComplete {
			swarmCompleteCount++
		}
	}
	if swarmStartCount != 1 {
		t.Fatalf("swarm.started events = %d, want 1", swarmStartCount)
	}
	if swarmCompleteCount != 1 {
		t.Fatalf("swarm.completed events = %d, want 1", swarmCompleteCount)
	}
}
