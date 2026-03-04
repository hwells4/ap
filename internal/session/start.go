package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/spec"
	"github.com/hwells4/ap/internal/stage"
)

const defaultProvider = "claude"

var (
	// ErrUnsupportedSpec is returned when Start receives a spec kind that
	// cannot be launched by the current runner contract.
	ErrUnsupportedSpec = errors.New("session: unsupported spec")
	// ErrLauncherRequired is returned when Start is called without a launcher.
	ErrLauncherRequired = errors.New("session: launcher is required")
)

// Session describes a launched child session.
type Session struct {
	Name           string
	RunDir         string
	RunRequestPath string
	Handle         SessionHandle
}

// StartOpts controls session.Start behavior.
type StartOpts struct {
	ProjectRoot   string
	Provider      string
	Model         string
	OnEscalate    string
	Context       string
	InputFiles    []string
	ParentSession string
	Force         bool
	Env           map[string]string
	Executable    string
	Launcher      Launcher
	LauncherOpts  StartOptions
}

type runRequestFile struct {
	Session        string            `json:"session"`
	Stage          string            `json:"stage"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model,omitempty"`
	Iterations     int               `json:"iterations"`
	PromptTemplate string            `json:"prompt_template"`
	WorkDir        string            `json:"work_dir"`
	Env            map[string]string `json:"env,omitempty"`
	RunDir         string            `json:"run_dir"`
	InputFiles     []string          `json:"input_files,omitempty"`
	OnEscalate     string            `json:"on_escalate,omitempty"`
	Context        string            `json:"context,omitempty"`
	Force          bool              `json:"force,omitempty"`
	ParentSession  string            `json:"parent_session,omitempty"`
}

// Start writes the session run request and delegates process creation to the
// configured Launcher.
func Start(parsed spec.Spec, session string, opts StartOpts) (*Session, error) {
	if parsed == nil {
		return nil, fmt.Errorf("session: parsed spec is required")
	}
	if opts.Launcher == nil {
		return nil, ErrLauncherRequired
	}

	projectRoot, err := resolveProjectRoot(opts.ProjectRoot)
	if err != nil {
		return nil, err
	}
	sessionName, err := validateSegment(session, "session name")
	if err != nil {
		return nil, err
	}

	if sessionDirExists(projectRoot, sessionName) {
		return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionName)
	}

	stageName, iterations, promptTemplate, err := resolveStageSpec(parsed, projectRoot)
	if err != nil {
		return nil, err
	}
	inputFiles, err := normalizeInputFiles(projectRoot, opts.InputFiles)
	if err != nil {
		return nil, err
	}

	layout, err := Prepare(projectRoot, sessionName, PrepareOptions{
		Force:      opts.Force,
		StageName:  stageName,
		StageIndex: 0,
		Iteration:  1,
	})
	if err != nil {
		return nil, fmt.Errorf("session: prepare layout: %w", err)
	}

	request := runRequestFile{
		Session:        sessionName,
		Stage:          stageName,
		Provider:       providerOrDefault(opts.Provider),
		Model:          strings.TrimSpace(opts.Model),
		Iterations:     iterations,
		PromptTemplate: promptTemplate,
		WorkDir:        projectRoot,
		Env:            cloneStringMap(opts.Env),
		RunDir:         layout.SessionDir,
		InputFiles:     inputFiles,
		OnEscalate:     strings.TrimSpace(opts.OnEscalate),
		Context:        opts.Context,
		Force:          opts.Force,
		ParentSession:  strings.TrimSpace(opts.ParentSession),
	}
	if err := writeRunRequest(layout.RunRequestPath, request); err != nil {
		return nil, err
	}

	cmd, err := runnerCommand(opts.Executable, sessionName, layout.RunRequestPath)
	if err != nil {
		return nil, err
	}

	launcherOpts := opts.LauncherOpts
	if strings.TrimSpace(launcherOpts.WorkDir) == "" {
		launcherOpts.WorkDir = projectRoot
	}

	handle, err := opts.Launcher.Start(sessionName, cmd, launcherOpts)
	if err != nil {
		if errors.Is(err, ErrSessionExists) {
			return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionName)
		}
		return nil, fmt.Errorf("session: launch via %s: %w", opts.Launcher.Name(), err)
	}

	return &Session{
		Name:           sessionName,
		RunDir:         layout.SessionDir,
		RunRequestPath: layout.RunRequestPath,
		Handle:         handle,
	}, nil
}

func resolveProjectRoot(projectRoot string) (string, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot != "" {
		return projectRoot, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("session: determine project root: %w", err)
	}
	return cwd, nil
}

func sessionDirExists(projectRoot, session string) bool {
	sessionDir := filepath.Join(projectRoot, ".ap", "runs", session)
	info, err := os.Stat(sessionDir)
	return err == nil && info.IsDir()
}

func resolveStageSpec(parsed spec.Spec, projectRoot string) (string, int, string, error) {
	stageSpec, ok := parsed.(spec.StageSpec)
	if !ok {
		return "", 0, "", fmt.Errorf("%w: %T", ErrUnsupportedSpec, parsed)
	}

	stageName := strings.TrimSpace(stageSpec.Name)
	if stageName == "" {
		return "", 0, "", fmt.Errorf("session: stage name is empty")
	}

	definition := stageSpec.Definition
	if definition.Name == "" {
		var err error
		definition, err = stage.ResolveStage(stageName, stage.ResolveOptions{
			ProjectRoot: projectRoot,
		})
		if err != nil {
			return "", 0, "", fmt.Errorf("session: resolve stage %q: %w", stageName, err)
		}
	}

	promptBytes, err := definition.ReadPrompt()
	if err != nil {
		return "", 0, "", fmt.Errorf("session: read stage prompt %q: %w", stageName, err)
	}

	iterations := stageSpec.Iterations
	if iterations <= 0 {
		iterations = 1
	}

	return stageName, iterations, string(promptBytes), nil
}

func providerOrDefault(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return defaultProvider
	}
	return provider
}

func normalizeInputFiles(projectRoot string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(files))
	for _, input := range files {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if !filepath.IsAbs(input) {
			input = filepath.Join(projectRoot, input)
		}
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, fmt.Errorf("session: normalize input file %q: %w", input, err)
		}
		normalized = append(normalized, abs)
	}
	return normalized, nil
}

func runnerCommand(executable, session, requestPath string) ([]string, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("session: resolve executable: %w", err)
		}
		executable = resolved
	}

	return []string{
		executable,
		"_run",
		"--session", session,
		"--request", requestPath,
	}, nil
}

func writeRunRequest(path string, req runRequestFile) error {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal run request: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("session: create run request dir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("session: write run request: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("session: rename run request: %w", err)
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
