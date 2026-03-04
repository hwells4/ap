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

func TestCompileParsesParallelBlocks(t *testing.T) {
	t.Parallel()

	path := writePipelineFile(t, `
name: parallel
nodes:
  - id: recommend
    parallel:
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
	parallel := pipeline.Nodes[0].Parallel
	if parallel == nil {
		t.Fatal("expected parallel block")
	}
	if len(parallel.Providers) != 2 {
		t.Fatalf("len(parallel.providers) = %d, want 2", len(parallel.Providers))
	}
	if parallel.Providers[0].Name != "claude" || parallel.Providers[1].Name != "codex" {
		t.Fatalf("unexpected provider names: %#v", parallel.Providers)
	}
	if len(parallel.Stages) != 1 || parallel.Stages[0].Stage != "recommend-improvements" {
		t.Fatalf("unexpected parallel stages: %#v", parallel.Stages)
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
