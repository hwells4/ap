package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/spec"
	"github.com/hwells4/ap/internal/stage"
)

func TestIntegration_Start_Success(t *testing.T) {
	projectRoot := t.TempDir()
	stageSpec := mustStageSpec(t, projectRoot, "ralph", 3)
	launcher := &stubLauncher{
		handle: SessionHandle{
			Session: "demo-session",
			PID:     4242,
			Backend: "stub",
		},
	}

	started, err := Start(stageSpec, "demo-session", StartOpts{
		ProjectRoot: projectRoot,
		Provider:    "claude",
		Model:       "opus",
		OnEscalate:  "exec:notify-send escalated",
		Context:     "focus auth",
		InputFiles:  []string{"docs/plan.md"},
		Executable:  "/usr/local/bin/ap",
		Launcher:    launcher,
		LauncherOpts: StartOptions{
			TimeoutSeconds: 12,
		},
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if started.Name != "demo-session" {
		t.Fatalf("session name = %q, want %q", started.Name, "demo-session")
	}
	if started.Handle.PID != 4242 {
		t.Fatalf("handle PID = %d, want 4242", started.Handle.PID)
	}

	if launcher.calls != 1 {
		t.Fatalf("launcher calls = %d, want 1", launcher.calls)
	}
	if launcher.session != "demo-session" {
		t.Fatalf("launcher session = %q, want %q", launcher.session, "demo-session")
	}

	if got, want := launcher.cmd[0], "/usr/local/bin/ap"; got != want {
		t.Fatalf("launcher cmd[0] = %q, want %q", got, want)
	}
	if got, want := launcher.cmd[1], "_run"; got != want {
		t.Fatalf("launcher cmd[1] = %q, want %q", got, want)
	}
	if got, want := launcher.opts.WorkDir, projectRoot; got != want {
		t.Fatalf("launcher opts.WorkDir = %q, want %q", got, want)
	}

	var request runRequestFile
	if err := readJSON(started.RunRequestPath, &request); err != nil {
		t.Fatalf("read request: %v", err)
	}

	if request.Session != "demo-session" {
		t.Fatalf("request.session = %q, want %q", request.Session, "demo-session")
	}
	if request.Stage != "ralph" {
		t.Fatalf("request.stage = %q, want %q", request.Stage, "ralph")
	}
	if request.Provider != "claude" {
		t.Fatalf("request.provider = %q, want %q", request.Provider, "claude")
	}
	if request.Model != "opus" {
		t.Fatalf("request.model = %q, want %q", request.Model, "opus")
	}
	if request.Context != "focus auth" {
		t.Fatalf("request.context = %q, want %q", request.Context, "focus auth")
	}
	if request.OnEscalate != "exec:notify-send escalated" {
		t.Fatalf("request.on_escalate = %q, want %q", request.OnEscalate, "exec:notify-send escalated")
	}
	if request.Iterations != 3 {
		t.Fatalf("request.iterations = %d, want 3", request.Iterations)
	}
	if request.ProjectRoot != projectRoot {
		t.Fatalf("request.project_root = %q, want %q", request.ProjectRoot, projectRoot)
	}
	if request.ConfigRoot != projectRoot {
		t.Fatalf("request.config_root = %q, want %q", request.ConfigRoot, projectRoot)
	}
	if request.ProjectKey != projectRoot {
		t.Fatalf("request.project_key = %q, want %q", request.ProjectKey, projectRoot)
	}
	if request.TargetSource != "cli" {
		t.Fatalf("request.target_source = %q, want cli", request.TargetSource)
	}
	if len(request.InputFiles) != 1 {
		t.Fatalf("request.input_files length = %d, want 1", len(request.InputFiles))
	}
	if !filepath.IsAbs(request.InputFiles[0]) {
		t.Fatalf("request.input_files[0] must be absolute, got %q", request.InputFiles[0])
	}
}

func TestIntegration_Start_DuplicateSession(t *testing.T) {
	projectRoot := t.TempDir()
	stageSpec := mustStageSpec(t, projectRoot, "ralph", 2)

	firstLauncher := &stubLauncher{
		handle: SessionHandle{
			Session: "dup-session",
			PID:     1001,
			Backend: "stub",
		},
	}
	if _, err := Start(stageSpec, "dup-session", StartOpts{
		ProjectRoot: projectRoot,
		Launcher:    firstLauncher,
	}); err != nil {
		t.Fatalf("first Start() error: %v", err)
	}

	secondLauncher := &stubLauncher{
		handle: SessionHandle{
			Session: "dup-session",
			PID:     1002,
			Backend: "stub",
		},
	}
	_, err := Start(stageSpec, "dup-session", StartOpts{
		ProjectRoot: projectRoot,
		Launcher:    secondLauncher,
	})
	if err == nil {
		t.Fatal("expected duplicate session error")
	}
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("error = %v, want ErrSessionExists", err)
	}
	if secondLauncher.calls != 0 {
		t.Fatalf("launcher should not be called for duplicate session, got %d calls", secondLauncher.calls)
	}
}

func mustStageSpec(t *testing.T, projectRoot, name string, iterations int) spec.StageSpec {
	t.Helper()

	definition, err := stage.ResolveStage(name, stage.ResolveOptions{
		ProjectRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("resolve stage %q: %v", name, err)
	}

	return spec.StageSpec{
		Name:       name,
		Iterations: iterations,
		Definition: definition,
	}
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

type stubLauncher struct {
	handle SessionHandle
	err    error

	calls   int
	session string
	cmd     []string
	opts    StartOptions
}

func (s *stubLauncher) Start(session string, runnerCmd []string, opts StartOptions) (SessionHandle, error) {
	s.calls++
	s.session = session
	s.cmd = append([]string(nil), runnerCmd...)
	s.opts = opts
	if s.err != nil {
		return SessionHandle{}, s.err
	}
	return s.handle, nil
}

func (s *stubLauncher) Kill(string) error { return nil }
func (s *stubLauncher) Available() bool   { return true }
func (s *stubLauncher) Name() string      { return "stub" }

func TestIntegration_Start_ChainSpec(t *testing.T) {
	projectRoot := t.TempDir()

	// Build a ChainSpec with two built-in stages.
	stages := []spec.StageSpec{
		mustStageSpec(t, projectRoot, "improve-plan", 2),
		mustStageSpec(t, projectRoot, "refine-tasks", 3),
	}
	chainSpec := spec.ChainSpec{Stages: stages}

	launcher := &stubLauncher{
		handle: SessionHandle{
			Session: "chain-session",
			PID:     5001,
			Backend: "stub",
		},
	}

	started, err := Start(chainSpec, "chain-session", StartOpts{
		ProjectRoot: projectRoot,
		Provider:    "claude",
		Executable:  "/usr/local/bin/ap",
		Launcher:    launcher,
	})
	if err != nil {
		t.Fatalf("Start(ChainSpec) error: %v", err)
	}

	if started.Name != "chain-session" {
		t.Fatalf("session name = %q, want %q", started.Name, "chain-session")
	}
	if launcher.calls != 1 {
		t.Fatalf("launcher calls = %d, want 1", launcher.calls)
	}

	var request runRequestFile
	if err := readJSON(started.RunRequestPath, &request); err != nil {
		t.Fatalf("read request: %v", err)
	}

	// Pipeline should be set.
	if request.Pipeline == nil {
		t.Fatal("request.Pipeline is nil, expected pipeline from ChainSpec")
	}
	if len(request.Pipeline.Nodes) != 2 {
		t.Fatalf("pipeline nodes = %d, want 2", len(request.Pipeline.Nodes))
	}
	if request.Pipeline.Nodes[0].Stage != "improve-plan" {
		t.Fatalf("pipeline node[0].Stage = %q, want improve-plan", request.Pipeline.Nodes[0].Stage)
	}
	if request.Pipeline.Nodes[1].Stage != "refine-tasks" {
		t.Fatalf("pipeline node[1].Stage = %q, want refine-tasks", request.Pipeline.Nodes[1].Stage)
	}

	// Stage should be first node's stage name.
	if request.Stage != "improve-plan" {
		t.Fatalf("request.Stage = %q, want improve-plan", request.Stage)
	}
	// Iterations=1 (runner manages real counts).
	if request.Iterations != 1 {
		t.Fatalf("request.Iterations = %d, want 1", request.Iterations)
	}
	// PromptTemplate should be empty (runner resolves per-stage).
	if request.PromptTemplate != "" {
		t.Fatalf("request.PromptTemplate = %q, want empty", request.PromptTemplate)
	}
}

func TestIntegration_Start_FileYAML(t *testing.T) {
	projectRoot := t.TempDir()

	// Create a minimal pipeline YAML file.
	pipelineDir := filepath.Join(projectRoot, "pipelines")
	if err := os.MkdirAll(pipelineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(pipelineDir, "test.yaml")
	yamlContent := `name: test-pipeline
nodes:
  - id: plan
    stage: improve-plan
    runs: 2
  - id: refine
    stage: refine-tasks
    runs: 1
    inputs:
      from: plan
      select: latest
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	fileSpec := spec.FileSpec{
		Path:     yamlPath,
		FileKind: spec.KindFileYAML,
	}

	launcher := &stubLauncher{
		handle: SessionHandle{
			Session: "yaml-session",
			PID:     5002,
			Backend: "stub",
		},
	}

	started, err := Start(fileSpec, "yaml-session", StartOpts{
		ProjectRoot: projectRoot,
		Provider:    "claude",
		Executable:  "/usr/local/bin/ap",
		Launcher:    launcher,
	})
	if err != nil {
		t.Fatalf("Start(FileSpec YAML) error: %v", err)
	}

	if started.Name != "yaml-session" {
		t.Fatalf("session name = %q, want %q", started.Name, "yaml-session")
	}

	var request runRequestFile
	if err := readJSON(started.RunRequestPath, &request); err != nil {
		t.Fatalf("read request: %v", err)
	}

	if request.Pipeline == nil {
		t.Fatal("request.Pipeline is nil, expected pipeline from FileSpec YAML")
	}
	if len(request.Pipeline.Nodes) != 2 {
		t.Fatalf("pipeline nodes = %d, want 2", len(request.Pipeline.Nodes))
	}
	if request.Pipeline.Name != "test-pipeline" {
		t.Fatalf("pipeline name = %q, want test-pipeline", request.Pipeline.Name)
	}
	if request.Stage != "improve-plan" {
		t.Fatalf("request.Stage = %q, want improve-plan", request.Stage)
	}
	if request.Iterations != 1 {
		t.Fatalf("request.Iterations = %d, want 1", request.Iterations)
	}
}

func TestIntegration_Start_UnsupportedSpec(t *testing.T) {
	projectRoot := t.TempDir()

	// FileSpec with KindFilePrompt is unsupported.
	promptPath := filepath.Join(projectRoot, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Do work"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileSpec := spec.FileSpec{
		Path:     promptPath,
		FileKind: spec.KindFilePrompt,
	}

	launcher := &stubLauncher{
		handle: SessionHandle{Session: "nope", PID: 1, Backend: "stub"},
	}

	_, err := Start(fileSpec, "nope-session", StartOpts{
		ProjectRoot: projectRoot,
		Launcher:    launcher,
	})
	if err == nil {
		t.Fatal("expected error for unsupported spec")
	}
	if !errors.Is(err, ErrUnsupportedSpec) {
		t.Fatalf("error = %v, want ErrUnsupportedSpec", err)
	}
	if launcher.calls != 0 {
		t.Fatalf("launcher should not be called for unsupported spec, got %d calls", launcher.calls)
	}
}

func TestIntegration_Start_ChainSpec_PipelineRoundTrip(t *testing.T) {
	projectRoot := t.TempDir()

	stages := []spec.StageSpec{
		mustStageSpec(t, projectRoot, "ralph", 5),
		mustStageSpec(t, projectRoot, "improve-plan", 3),
	}
	chainSpec := spec.ChainSpec{Stages: stages}

	launcher := &stubLauncher{
		handle: SessionHandle{Session: "roundtrip", PID: 6001, Backend: "stub"},
	}

	started, err := Start(chainSpec, "roundtrip", StartOpts{
		ProjectRoot: projectRoot,
		Launcher:    launcher,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	// Read the JSON, re-parse, and verify Pipeline survives serialization.
	data, err := os.ReadFile(started.RunRequestPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded runRequestFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Pipeline == nil {
		t.Fatal("round-tripped Pipeline is nil")
	}
	if len(decoded.Pipeline.Nodes) != 2 {
		t.Fatalf("round-tripped pipeline nodes = %d, want 2", len(decoded.Pipeline.Nodes))
	}
	// Chain should wire inputs.from on second node.
	if decoded.Pipeline.Nodes[1].Inputs.From == "" {
		t.Fatal("second node should have inputs.from set by chain wiring")
	}
	if decoded.Pipeline.Nodes[0].Runs != 5 {
		t.Fatalf("node[0].Runs = %d, want 5", decoded.Pipeline.Nodes[0].Runs)
	}
	if decoded.Pipeline.Nodes[1].Runs != 3 {
		t.Fatalf("node[1].Runs = %d, want 3", decoded.Pipeline.Nodes[1].Runs)
	}
}

// Verify compile.Pipeline is not needed as unused import.
var _ = compile.Pipeline{}
