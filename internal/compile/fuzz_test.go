package compile

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzCompile exercises the YAML pipeline compiler with arbitrary input.
// It writes the fuzzed bytes to a temp file and calls Compile.
func FuzzCompile(f *testing.F) {
	// Valid minimal pipeline
	f.Add([]byte("name: test\nnodes:\n  - id: first\n    stage: improve-plan\n"))

	// Pipeline with two nodes and inputs wiring
	f.Add([]byte(`name: refine
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
`))

	// Pipeline using deprecated stages key
	f.Add([]byte("name: old\nstages:\n  - id: a\n    stage: improve-plan\n"))

	// Parallel block
	f.Add([]byte(`name: parallel
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
`))

	// Malformed YAML
	f.Add([]byte(""))
	f.Add([]byte("not yaml at all {{{"))
	f.Add([]byte("name:\nnodes:"))
	f.Add([]byte("name: test\n"))
	f.Add([]byte("name: test\nnodes: not-a-list\n"))
	f.Add([]byte("name: test\nnodes:\n  - id: \"\"\n    stage: x\n"))
	f.Add([]byte("name: test\nnodes:\n  - id: a\n    stage: a\n  - id: a\n    stage: b\n"))
	f.Add([]byte("[1, 2, 3]"))
	f.Add([]byte("null"))

	// Both nodes and stages set (conflict)
	f.Add([]byte("name: both\nnodes:\n  - id: a\n    stage: x\nstages:\n  - id: b\n    stage: y\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "pipeline.yaml")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Skip("could not write temp file")
		}
		_, _ = Compile(path)
	})
}
