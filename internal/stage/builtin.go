package stage

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	builtinassets "github.com/hwells4/ap/stages"
)

const embeddedPathPrefix = "embedded://stages/"

// LoadBuiltinDefinitions discovers built-in stage definitions from embedded assets.
func LoadBuiltinDefinitions() (map[string]Definition, error) {
	entries, err := fs.ReadDir(builtinassets.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded stages: %w", err)
	}

	definitions := make(map[string]Definition)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		stageName := strings.TrimSpace(entry.Name())
		if stageName == "" {
			continue
		}

		configPath := path.Join(stageName, "stage.yaml")
		if !embeddedFileExists(configPath) {
			continue
		}

		promptPath, err := resolveEmbeddedPromptPath(stageName, configPath)
		if err != nil {
			return nil, err
		}

		definitions[stageName] = Definition{
			Name:               stageName,
			Dir:                embeddedPathPrefix + stageName,
			ConfigPath:         embeddedPathPrefix + configPath,
			PromptPath:         embeddedPathPrefix + promptPath,
			embeddedFS:         builtinassets.FS,
			embeddedConfigPath: configPath,
			embeddedPromptPath: promptPath,
		}
	}
	return definitions, nil
}

// BuiltinStageNames returns embedded built-in stage names in sorted order.
func BuiltinStageNames() ([]string, error) {
	definitions, err := LoadBuiltinDefinitions()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func resolveEmbeddedPromptPath(stageName, configPath string) (string, error) {
	configContent, err := fs.ReadFile(builtinassets.FS, configPath)
	if err != nil {
		return "", fmt.Errorf("read embedded config for stage %s: %w", stageName, err)
	}

	promptField, err := readPromptFieldReader(strings.NewReader(string(configContent)))
	if err != nil {
		return "", fmt.Errorf("read prompt field for stage %s: %w", stageName, err)
	}

	var candidates []string
	if promptField != "" && promptField != "null" {
		candidate := promptField
		if !path.IsAbs(candidate) {
			candidate = path.Join(stageName, candidate)
		}
		candidates = append(candidates, path.Clean(candidate))
	}
	candidates = append(candidates, path.Join(stageName, "prompt.md"))

	for _, candidate := range candidates {
		if embeddedFileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("embedded stage %s has no prompt.md or prompt: field", stageName)
}

func embeddedFileExists(filePath string) bool {
	info, err := fs.Stat(builtinassets.FS, filePath)
	return err == nil && !info.IsDir()
}
