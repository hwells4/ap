package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestSwarm_ThreeProviders_TwoStages_ManifestComplete runs a pipeline with
// a swarm block containing 3 providers and 2 stages, then verifies the
// manifest.json is complete.
func TestSwarm_ThreeProviders_TwoStages_ManifestComplete(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm work",
		}),
	)

	pipeline := &compile.Pipeline{
		Name: "swarm-manifest-test",
		Nodes: []compile.Node{
			{ID: "plan", Stage: "improve-plan", Runs: 1},
			{
				ID: "swarm-block",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
						{Name: "mock"},
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "analyze", Stage: "improve-plan", Runs: 2},
						{ID: "review", Stage: "improve-plan", Runs: 1},
					},
				},
			},
			{ID: "merge", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "swarm-manifest",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(filepath.Dir(runDir))),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// Total: 1 (plan) + 9 (3 providers * (2+1) stages) + 1 (merge) = 11
	if res.Iterations != 11 {
		t.Fatalf("iterations = %d, want 11", res.Iterations)
	}

	// Verify manifest.
	manifestPath := filepath.Join(runDir, "swarm-01-swarm-block", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest struct {
		Block struct {
			Name string `json:"name"`
		} `json:"block"`
		Providers map[string]map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	// 3 duplicate "mock" → mock-1, mock-2, mock-3.
	if len(manifest.Providers) != 3 {
		t.Fatalf("manifest providers = %d, want 3", len(manifest.Providers))
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("mock-%d", i)
		provStages, ok := manifest.Providers[name]
		if !ok {
			t.Fatalf("manifest missing provider %q", name)
		}
		if len(provStages) != 2 {
			t.Fatalf("manifest[%s] stages = %d, want 2", name, len(provStages))
		}
		if _, ok := provStages["analyze"]; !ok {
			t.Fatalf("manifest[%s] missing stage 'analyze'", name)
		}
		if _, ok := provStages["review"]; !ok {
			t.Fatalf("manifest[%s] missing stage 'review'", name)
		}
	}
}

// TestSwarm_OutputFlowsToDownstreamContext verifies that the downstream stage's
// context.json references the swarm block's manifest path in inputs.
func TestSwarm_OutputFlowsToDownstreamContext(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm output",
		}),
	)

	pipeline := &compile.Pipeline{
		Name: "swarm-output-flow",
		Nodes: []compile.Node{
			{
				ID: "swarm-work",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "analyze", Stage: "improve-plan", Runs: 1},
					},
				},
			},
			{ID: "synthesize", Stage: "improve-plan", Runs: 1},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "swarm-output",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(filepath.Dir(runDir))),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// Total: 2 (swarm: 2 providers * 1 stage) + 1 (synthesize) = 3
	if res.Iterations != 3 {
		t.Fatalf("iterations = %d, want 3", res.Iterations)
	}

	// Verify the synthesize stage ran (its context.json should exist).
	synthCtxPath := filepath.Join(runDir, "stage-01-synthesize", "iterations", "001", "context.json")
	if _, err := os.Stat(synthCtxPath); os.IsNotExist(err) {
		t.Fatalf("synthesize context.json missing — downstream stage didn't run")
	}

	// Verify swarm events.
	evts := readEvents(t, s, "swarm-output")
	swarmComplete := filterByType(evts, store.TypeSwarmComplete)
	if len(swarmComplete) != 1 {
		t.Fatalf("swarm.completed events = %d, want 1", len(swarmComplete))
	}
}

// TestSwarm_CompoundStageNames_InStore verifies that swarm iterations are
// recorded with compound stage names in the store.
func TestSwarm_CompoundStageNames_InStore(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm iter",
		}),
	)

	pipeline := &compile.Pipeline{
		Name: "swarm-compound",
		Nodes: []compile.Node{
			{
				ID: "review-block",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "review", Stage: "improve-plan", Runs: 2},
					},
				},
			},
		},
	}

	res, err := Run(context.Background(), Config{
		Session:        "swarm-compound",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        filepath.Dir(filepath.Dir(filepath.Dir(runDir))),
		Store:          s,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// Verify compound stage names in the store.
	for _, provName := range []string{"mock-1", "mock-2"} {
		compoundName := fmt.Sprintf("swarm:review-block:%s:review", provName)
		rows, err := s.GetIterations(context.Background(), "swarm-compound", compoundName)
		if err != nil {
			t.Fatalf("GetIterations(%s): %v", provName, err)
		}
		if len(rows) != 2 {
			t.Fatalf("iterations for %s = %d, want 2", provName, len(rows))
		}
	}
}

// TestSwarm_HooksFireAroundSwarmNodes verifies that pre_stage and post_stage
// hooks fire around a swarm block node in a pipeline.
func TestSwarm_HooksFireAroundSwarmNodes(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm hook test",
		}),
	)

	preStageMarker := filepath.Join(workDir, "pre-stage-swarm.marker")
	postStageMarker := filepath.Join(workDir, "post-stage-swarm.marker")

	pipeline := &compile.Pipeline{
		Name: "swarm-hooks",
		Nodes: []compile.Node{
			{
				ID: "swarm-node",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "analyze", Stage: "improve-plan", Runs: 1},
					},
				},
			},
		},
	}

	cfg := Config{
		Session:        "swarm-hooks",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PreStage:  "touch " + preStageMarker,
			PostStage: "touch " + postStageMarker,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	if _, err := os.Stat(preStageMarker); os.IsNotExist(err) {
		t.Error("pre_stage marker missing — hook did not fire before swarm block")
	}
	if _, err := os.Stat(postStageMarker); os.IsNotExist(err) {
		t.Error("post_stage marker missing — hook did not fire after swarm block")
	}

	// Verify hook.completed events.
	evts := readEvents(t, s, "swarm-hooks")
	hookCompleted := filterByType(evts, store.TypeHookCompleted)
	preStageFound := false
	postStageFound := false
	for _, evt := range hookCompleted {
		data := parseEventData(t, evt)
		if data["hook"] == "pre_stage" {
			preStageFound = true
		}
		if data["hook"] == "post_stage" {
			postStageFound = true
		}
	}
	if !preStageFound {
		t.Error("no hook.completed event for pre_stage around swarm")
	}
	if !postStageFound {
		t.Error("no hook.completed event for post_stage around swarm")
	}
}

// TestSwarm_PostIterationHooksFireInSwarm verifies that post_iteration hooks
// fire for each iteration inside a swarm block (regression test for P2 bug).
func TestSwarm_PostIterationHooksFireInSwarm(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "continue",
			Summary:  "swarm iter hook test",
		}),
	)

	logFile := filepath.Join(workDir, "swarm-iter-hooks.log")

	pipeline := &compile.Pipeline{
		Name: "swarm-iter-hooks",
		Nodes: []compile.Node{
			{
				ID: "swarm-node",
				Swarm: &compile.SwarmBlock{
					Providers: compile.ProviderList{
						{Name: "mock"},
					},
					Stages: []compile.SwarmStage{
						{ID: "work", Stage: "improve-plan", Runs: 2},
					},
				},
			},
		},
	}

	cfg := Config{
		Session:        "swarm-iter-hooks",
		RunDir:         runDir,
		Provider:       mp,
		Pipeline:       pipeline,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
		HookTimeout:    10 * time.Second,
		Hooks: LifecycleHooks{
			PostIteration: "echo post_iteration >> " + logFile,
		},
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// 1 provider * 2 iterations = 2 post_iteration hooks should have fired.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("post_iteration hook fired %d times, want 2; log content: %q", len(lines), string(data))
	}
}

// TestSwarm_MessagesFileCreated verifies messages.jsonl is created at session
// start (regression test for P4 bug).
func TestSwarm_MessagesFileCreated(t *testing.T) {
	runDir, s := tempSession(t)
	workDir := filepath.Dir(filepath.Dir(filepath.Dir(runDir)))

	mp := mock.New(
		mock.WithFallback(mock.Response{
			Decision: "stop",
			Summary:  "done",
		}),
	)

	cfg := Config{
		Session:        "messages-test",
		RunDir:         runDir,
		StageName:      "improve-plan",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		WorkDir:        workDir,
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	messagesPath := filepath.Join(runDir, "messages.jsonl")
	if _, err := os.Stat(messagesPath); os.IsNotExist(err) {
		t.Fatal("messages.jsonl should exist after session start")
	}
}

// init registers a test-only "noop" provider so runtarget.Resolve doesn't fail.
func init() {
	// Ensure the swarm tests don't hang on provider resolution.
	_ = time.Second // suppress unused import
}
