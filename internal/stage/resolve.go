// Package stage resolves stage configuration and prompt locations.
package stage

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hwells4/ap/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// Definition describes a resolved stage configuration.
type Definition struct {
	Name       string
	Dir        string
	ConfigPath string
	PromptPath string

	embeddedFS         fs.FS
	embeddedConfigPath string
	embeddedPromptPath string
}

// IsEmbedded reports whether this definition is backed by an embedded filesystem.
func (d Definition) IsEmbedded() bool {
	return d.embeddedFS != nil
}

// ReadConfig loads stage.yaml content from filesystem or embedded assets.
func (d Definition) ReadConfig() ([]byte, error) {
	if d.embeddedFS != nil {
		return fs.ReadFile(d.embeddedFS, d.embeddedConfigPath)
	}
	return os.ReadFile(d.ConfigPath)
}

// ReadPrompt loads prompt content from filesystem or embedded assets.
func (d Definition) ReadPrompt() ([]byte, error) {
	if d.embeddedFS != nil {
		return fs.ReadFile(d.embeddedFS, d.embeddedPromptPath)
	}
	return os.ReadFile(d.PromptPath)
}

// ReadOutputPath returns the output_path value from stage.yaml, if set.
// Returns empty string when output_path is not configured.
func (d Definition) ReadOutputPath() string {
	configData, err := d.ReadConfig()
	if err != nil {
		return ""
	}
	var doc struct {
		OutputPath string `yaml:"output_path"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(configData)).Decode(&doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.OutputPath)
}

// ReadHooks returns the hooks map from stage.yaml, if set.
// Returns nil when hooks are not configured.
func (d Definition) ReadHooks() map[string]string {
	configData, err := d.ReadConfig()
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string]string `yaml:"hooks"`
	}
	if yaml.NewDecoder(bytes.NewReader(configData)).Decode(&doc) != nil {
		return nil
	}
	return doc.Hooks
}

// ResolveOptions defines search roots for stage resolution.
type ResolveOptions struct {
	ProjectRoot        string
	PipelineDir        string
	AgentPipelinesRoot string
	BuiltinDefinitions map[string]Definition
}

// ResolveStage locates a stage definition by name using precedence rules.
func ResolveStage(name string, opts ResolveOptions) (Definition, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Definition{}, fmt.Errorf("stage name is empty")
	}

	if strings.TrimSpace(opts.AgentPipelinesRoot) == "" {
		opts.AgentPipelinesRoot = os.Getenv("AGENT_PIPELINES_ROOT")
	}

	var candidates []string
	addCandidate := func(path string) {
		if strings.TrimSpace(path) != "" {
			candidates = append(candidates, path)
		}
	}

	if strings.TrimSpace(opts.ProjectRoot) != "" {
		addCandidate(filepath.Join(opts.ProjectRoot, ".ap", "stages", name, "stage.yaml"))
	}
	if strings.TrimSpace(opts.PipelineDir) != "" {
		addCandidate(filepath.Join(opts.PipelineDir, "stages", name, "stage.yaml"))
	}
	if strings.TrimSpace(opts.AgentPipelinesRoot) != "" {
		addCandidate(filepath.Join(opts.AgentPipelinesRoot, "scripts", "stages", name, "stage.yaml"))
	}

	// User-global stages: ~/.config/ap/stages/{name}/stage.yaml
	if home, err := os.UserHomeDir(); err == nil {
		addCandidate(filepath.Join(home, ".config", "ap", "stages", name, "stage.yaml"))
	}

	for _, candidate := range candidates {
		if fsutil.FileExists(candidate) {
			return definitionFromPath(name, candidate)
		}
	}

	builtinDefinitions := opts.BuiltinDefinitions
	if builtinDefinitions == nil {
		loaded, err := LoadBuiltinDefinitions()
		if err != nil {
			return Definition{}, fmt.Errorf("load built-in stages: %w", err)
		}
		builtinDefinitions = loaded
	}
	if def, ok := builtinDefinitions[name]; ok {
		return def, nil
	}

	if len(candidates) == 0 {
		return Definition{}, fmt.Errorf("stage %q not found; no search paths configured", name)
	}

	return Definition{}, fmt.Errorf("stage %q not found; searched: %s", name, strings.Join(candidates, ", "))
}

func definitionFromPath(name, configPath string) (Definition, error) {
	stageDir := filepath.Dir(configPath)
	promptPath, err := resolvePromptPath(stageDir, configPath, name)
	if err != nil {
		return Definition{}, err
	}
	return Definition{
		Name:       name,
		Dir:        stageDir,
		ConfigPath: configPath,
		PromptPath: promptPath,
	}, nil
}

func resolvePromptPath(stageDir, configPath, stageName string) (string, error) {
	promptField, err := readPromptField(configPath)
	if err != nil {
		return "", fmt.Errorf("read prompt field: %w", err)
	}

	var candidates []string
	if promptField != "" && promptField != "null" {
		candidate := promptField
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(stageDir, candidate)
		}
		candidates = append(candidates, filepath.Clean(candidate))
	}
	candidates = append(candidates, filepath.Join(stageDir, "prompt.md"))

	for _, candidate := range candidates {
		if fsutil.FileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("stage %s has no prompt.md or prompt: field", stageName)
}

func readPromptField(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	return readPromptFieldReader(file)
}

func readPromptFieldReader(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.TrimLeft(line, " \t") != line {
			continue
		}
		if !strings.HasPrefix(trimmed, "prompt:") {
			continue
		}

		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "prompt:"))
		if raw == "" || raw == "null" || raw == "~" {
			return "", nil
		}
		if strings.HasPrefix(raw, "|") || strings.HasPrefix(raw, ">") {
			return "", nil
		}

		raw = strings.TrimSpace(stripInlineComment(raw))
		if raw == "" || raw == "null" || raw == "~" {
			return "", nil
		}

		if strings.HasPrefix(raw, "\"") {
			if unquoted, err := strconv.Unquote(raw); err == nil {
				return unquoted, nil
			}
		}
		if strings.HasPrefix(raw, "'") {
			return unquoteSingle(raw), nil
		}
		return raw, nil
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func stripInlineComment(value string) string {
	if value == "" {
		return value
	}
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
		return value
	}
	if idx := strings.Index(value, " #"); idx != -1 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}

func unquoteSingle(value string) string {
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return value
	}
	inner := value[1 : len(value)-1]
	return strings.ReplaceAll(inner, "''", "'")
}
