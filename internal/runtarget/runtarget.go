package runtarget

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	SourceCLI           = "cli"
	SourceSpawnInherit  = "spawn_inherit"
	SourceSpawnOverride = "spawn_override"
)

// Target captures immutable spawn-location metadata for a session run.
type Target struct {
	ProjectRoot string `json:"project_root"`
	RepoRoot    string `json:"repo_root,omitempty"`
	ConfigRoot  string `json:"config_root,omitempty"`
	ProjectKey  string `json:"project_key"`
	Source      string `json:"source,omitempty"`
}

// Resolve builds a normalized Target from a project root and source.
// If projectRoot is empty, the current working directory is used.
func Resolve(projectRoot, source string) (Target, error) {
	root, err := normalizeProjectRoot(projectRoot)
	if err != nil {
		return Target{}, err
	}
	repoRoot, _ := detectRepoRoot(root)
	configRoot := detectConfigRoot(root, repoRoot)
	projectKey := buildProjectKey(root)

	source = strings.TrimSpace(source)
	if source == "" {
		source = SourceCLI
	}

	return Target{
		ProjectRoot: root,
		RepoRoot:    repoRoot,
		ConfigRoot:  configRoot,
		ProjectKey:  projectKey,
		Source:      source,
	}, nil
}

// NormalizeWithDefaults fills and normalizes missing fields from a partial target.
func NormalizeWithDefaults(target Target, source string) (Target, error) {
	resolved, err := Resolve(target.ProjectRoot, source)
	if err != nil {
		return Target{}, err
	}

	if strings.TrimSpace(target.RepoRoot) != "" {
		resolved.RepoRoot = strings.TrimSpace(target.RepoRoot)
	}
	if strings.TrimSpace(target.ConfigRoot) != "" {
		resolved.ConfigRoot = strings.TrimSpace(target.ConfigRoot)
	}
	if strings.TrimSpace(target.ProjectKey) != "" {
		resolved.ProjectKey = strings.TrimSpace(target.ProjectKey)
	}
	if strings.TrimSpace(target.Source) != "" {
		resolved.Source = strings.TrimSpace(target.Source)
	}

	return resolved, nil
}

// ResolveSpawnRoot resolves an optional spawn override against a parent project root.
func ResolveSpawnRoot(parentProjectRoot, override string) (string, error) {
	parentProjectRoot = strings.TrimSpace(parentProjectRoot)
	if parentProjectRoot == "" {
		return "", fmt.Errorf("run target: parent project root is required")
	}
	override = strings.TrimSpace(override)
	if override == "" {
		return parentProjectRoot, nil
	}
	if !filepath.IsAbs(override) {
		override = filepath.Join(parentProjectRoot, override)
	}
	root, err := filepath.Abs(override)
	if err != nil {
		return "", fmt.Errorf("run target: normalize spawn project root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("run target: spawn project root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("run target: spawn project root %q is not a directory", root)
	}
	return root, nil
}

func normalizeProjectRoot(projectRoot string) (string, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("run target: determine project root: %w", err)
		}
		projectRoot = cwd
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("run target: normalize project root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("run target: project root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("run target: project root %q is not a directory", abs)
	}
	return abs, nil
}

func detectRepoRoot(projectRoot string) (string, bool) {
	dir := projectRoot
	for {
		if exists(filepath.Join(dir, ".git")) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func detectConfigRoot(projectRoot, repoRoot string) string {
	if hasConfig(projectRoot) {
		return projectRoot
	}
	if repoRoot != "" && hasConfig(repoRoot) {
		return repoRoot
	}
	return projectRoot
}

func hasConfig(root string) bool {
	if root == "" {
		return false
	}
	if exists(filepath.Join(root, "AGENTS.md")) {
		return true
	}
	if exists(filepath.Join(root, ".claude")) {
		return true
	}
	return false
}

func buildProjectKey(projectRoot string) string {
	return filepath.Clean(strings.TrimSpace(projectRoot))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
