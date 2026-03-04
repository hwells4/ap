// Package session provides deterministic session directory layout helpers.
package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	stageDirFormat     = "stage-%02d-%s"
	iterationDirFormat = "%03d"
)

// PrepareOptions configures session directory creation.
type PrepareOptions struct {
	Force      bool
	StageName  string
	StageIndex int
	Iteration  int
}

// Layout describes the canonical session directory structure.
type Layout struct {
	ProjectRoot  string
	RunsRoot     string
	SessionName  string
	SessionDir   string
	StageDir     string
	IterationDir string

	RunRequestPath string
	StatePath      string
	EventsPath     string
	ProgressPath   string
	OutputPath     string
	StatusPath     string
	ResultPath     string
}

// Prepare creates the deterministic session directory tree under .ap/runs/<session>.
// New sessions are created via temp-dir + rename for crash-safe setup. Existing sessions
// are reused idempotently, and Force recreates the tree from scratch.
func Prepare(projectRoot, sessionName string, opts PrepareOptions) (Layout, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return Layout{}, errors.New("project root is empty")
	}
	sessionSegment, err := validateSegment(sessionName, "session name")
	if err != nil {
		return Layout{}, err
	}

	stage := strings.TrimSpace(opts.StageName)
	if stage == "" {
		stage = "default"
	}
	stageSegment, err := validateSegment(stage, "stage name")
	if err != nil {
		return Layout{}, err
	}

	stageIndex := opts.StageIndex
	if stageIndex < 0 {
		stageIndex = 0
	}
	iteration := opts.Iteration
	if iteration <= 0 {
		iteration = 1
	}

	layout := buildLayout(projectRoot, sessionSegment, stageSegment, stageIndex, iteration)

	if err := os.MkdirAll(layout.RunsRoot, 0o755); err != nil {
		return Layout{}, fmt.Errorf("create runs root: %w", err)
	}

	if opts.Force {
		if err := os.RemoveAll(layout.SessionDir); err != nil {
			return Layout{}, fmt.Errorf("reset session dir: %w", err)
		}
	}

	if exists(layout.SessionDir) {
		if err := ensureLayoutDirs(layout); err != nil {
			return Layout{}, err
		}
		return layout, nil
	}

	tmpRoot, err := os.MkdirTemp(layout.RunsRoot, "."+sessionSegment+".tmp-*")
	if err != nil {
		return Layout{}, fmt.Errorf("create session temp dir: %w", err)
	}
	tmpLayout := buildLayout(projectRoot, filepath.Base(tmpRoot), stageSegment, stageIndex, iteration)

	if err := ensureLayoutDirs(tmpLayout); err != nil {
		_ = os.RemoveAll(tmpRoot)
		return Layout{}, err
	}

	if err := os.Rename(tmpRoot, layout.SessionDir); err != nil {
		_ = os.RemoveAll(tmpRoot)
		if exists(layout.SessionDir) {
			if err := ensureLayoutDirs(layout); err != nil {
				return Layout{}, err
			}
			return layout, nil
		}
		return Layout{}, fmt.Errorf("promote session dir: %w", err)
	}

	return layout, nil
}

func buildLayout(projectRoot, sessionName, stageName string, stageIndex, iteration int) Layout {
	runsRoot := filepath.Join(projectRoot, ".ap", "runs")
	sessionDir := filepath.Join(runsRoot, sessionName)
	stageDir := filepath.Join(sessionDir, fmt.Sprintf(stageDirFormat, stageIndex, stageName))
	iterationDir := filepath.Join(stageDir, "iterations", fmt.Sprintf(iterationDirFormat, iteration))

	return Layout{
		ProjectRoot:  projectRoot,
		RunsRoot:     runsRoot,
		SessionName:  sessionName,
		SessionDir:   sessionDir,
		StageDir:     stageDir,
		IterationDir: iterationDir,

		RunRequestPath: filepath.Join(sessionDir, "run_request.json"),
		StatePath:      filepath.Join(sessionDir, "state.json"),
		EventsPath:     filepath.Join(sessionDir, "events.jsonl"),
		ProgressPath:   filepath.Join(stageDir, "progress.md"),
		OutputPath:     filepath.Join(iterationDir, "output.md"),
		StatusPath:     filepath.Join(iterationDir, "status.json"),
		ResultPath:     filepath.Join(iterationDir, "result.json"),
	}
}

func ensureLayoutDirs(layout Layout) error {
	for _, path := range []string{
		layout.SessionDir,
		layout.StageDir,
		layout.IterationDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create layout dir %q: %w", path, err)
		}
	}
	return nil
}

func validateSegment(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is empty", field)
	}
	if value == "." || value == ".." {
		return "", fmt.Errorf("%s is invalid", field)
	}
	if strings.Contains(value, string(os.PathSeparator)) || strings.Contains(value, `\`) {
		return "", fmt.Errorf("%s contains path separators", field)
	}
	if filepath.Clean(value) != value {
		return "", fmt.Errorf("%s must be clean path segment", field)
	}
	return value, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
