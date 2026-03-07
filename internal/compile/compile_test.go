package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileParsesNodes(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: refine
description: refine pipeline
nodes:
  - id: improve-plan
    stage: improve-plan
    runs: 5
  - id: refine-tasks
    stage: refine-tasks
    runs: 3
    inputs:
      from: improve-plan
      select: latest
`)

	pipeline, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if pipeline.Name != "refine" {
		t.Fatalf("Name = %q, want refine", pipeline.Name)
	}
	if len(pipeline.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(pipeline.Nodes))
	}
	if pipeline.Nodes[1].Inputs.From != "improve-plan" {
		t.Fatalf("nodes[1].inputs.from = %q, want improve-plan", pipeline.Nodes[1].Inputs.From)
	}
	if pipeline.Nodes[1].Inputs.Select != SelectLatest {
		t.Fatalf("nodes[1].inputs.select = %q, want %q", pipeline.Nodes[1].Inputs.Select, SelectLatest)
	}
}

func TestCompileAcceptsStagesAlias(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: alias-pipeline
stages:
  - id: first
    stage: improve-plan
    runs: 1
  - id: second
    stage: refine-tasks
    runs: 1
    inputs:
      from: first
      select: all
`)

	pipeline, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pipeline.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(pipeline.Nodes))
	}
	if pipeline.Nodes[1].Inputs.Select != SelectAll {
		t.Fatalf("nodes[1].inputs.select = %q, want %q", pipeline.Nodes[1].Inputs.Select, SelectAll)
	}
}

func TestCompileRejectsUnknownStageReference(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: bad-stage
nodes:
  - id: first
    stage: stage-does-not-exist
`)

	_, err := Compile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `references unknown stage "stage-does-not-exist"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileRejectsUnknownInputReference(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: bad-input
nodes:
  - id: first
    stage: improve-plan
    inputs:
      from: missing
`)

	_, err := Compile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `inputs.from "missing" does not match any node id`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileRejectsInvalidSelect(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: bad-select
nodes:
  - id: first
    stage: improve-plan
    inputs:
      select: newest
`)

	_, err := Compile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `inputs.select must be one of "latest" or "all"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileDetectsCircularDependencies(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: cycle
nodes:
  - id: first
    stage: improve-plan
    inputs:
      from: second
  - id: second
    stage: refine-tasks
    inputs:
      from: first
`)

	_, err := Compile(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "circular dependency detected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileParsesSwarmBlocks(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: swarm-test
nodes:
  - id: recommend
    swarm:
      providers: [claude, codex]
      stages:
        - id: recs
          stage: recommend-improvements
          termination:
            type: fixed
            iterations: 1
  - id: implement
    stage: implement-changes
    inputs:
      from: recommend
`)

	pipeline, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pipeline.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2", len(pipeline.Nodes))
	}
	swarmBlock := pipeline.Nodes[0].Swarm
	if swarmBlock == nil {
		t.Fatal("expected swarm block")
	}
	if len(swarmBlock.Providers) != 2 {
		t.Fatalf("len(swarm.providers) = %d, want 2", len(swarmBlock.Providers))
	}
	if swarmBlock.Providers[0].Name != "claude" || swarmBlock.Providers[1].Name != "codex" {
		t.Fatalf("unexpected provider names: %#v", swarmBlock.Providers)
	}
	if len(swarmBlock.Stages) != 1 || swarmBlock.Stages[0].Stage != "recommend-improvements" {
		t.Fatalf("unexpected swarm stages: %#v", swarmBlock.Stages)
	}
}

func TestCompileParsesPipelineHooks(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: hook-pipeline
hooks:
  pre_session: "git checkout -b work/${SESSION}"
  post_iteration: "git add -A && git commit -m 'iter ${ITERATION}'"
  post_session: "git push -u origin HEAD"
nodes:
  - id: improve-plan
    stage: improve-plan
    runs: 3
`)

	pipeline, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(pipeline.Hooks) != 3 {
		t.Fatalf("len(Hooks) = %d, want 3", len(pipeline.Hooks))
	}
	if pipeline.Hooks["pre_session"] != "git checkout -b work/${SESSION}" {
		t.Fatalf("pre_session = %q", pipeline.Hooks["pre_session"])
	}
	if pipeline.Hooks["post_iteration"] != "git add -A && git commit -m 'iter ${ITERATION}'" {
		t.Fatalf("post_iteration = %q", pipeline.Hooks["post_iteration"])
	}
	if pipeline.Hooks["post_session"] != "git push -u origin HEAD" {
		t.Fatalf("post_session = %q", pipeline.Hooks["post_session"])
	}
}

func TestCompileNoHooksDefaultsToEmptyMap(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: no-hooks
nodes:
  - id: improve-plan
    stage: improve-plan
    runs: 1
`)

	pipeline, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if pipeline.Hooks == nil {
		t.Fatal("Hooks should not be nil, want empty map")
	}
	if len(pipeline.Hooks) != 0 {
		t.Fatalf("len(Hooks) = %d, want 0", len(pipeline.Hooks))
	}
}

func TestCompileSamplesFromRepo(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{
		"pipelines/refine.yaml",
		"pipelines/plan-debate.yaml",
	} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			path := filepath.Clean(filepath.Join("..", "..", rel))
			_, err := Compile(path)
			if err != nil {
				t.Fatalf("Compile(%s) error = %v", rel, err)
			}
		})
	}
}

func writePipelineFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	writeFile(t, path, body)
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
