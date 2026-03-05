package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
