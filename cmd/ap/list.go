package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/stage"
	"github.com/hwells4/ap/internal/store"
	"gopkg.in/yaml.v3"
)

type stageEntry struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Source      string      `json:"source"`
	Stats       *stageStats `json:"stats,omitempty"`
}

type stageStats struct {
	TotalSessions   int     `json:"total_sessions"`
	TotalIterations int     `json:"total_iterations"`
	Completed       int     `json:"completed"`
	Failed          int     `json:"failed"`
	AvgIterations   float64 `json:"avg_iterations"`
	LastRun         string  `json:"last_run"`
}

func runList(args []string, deps cliDeps) int {
	for _, arg := range args {
		if arg == "--json" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return renderError(deps, output.ExitInvalidArgs, output.NewError(
				"UNKNOWN_FLAG",
				fmt.Sprintf("unknown flag %q for list", arg),
				"Only --json is supported.",
				"ap list [--json]",
				[]string{"ap list", "ap list --json"},
			))
		}
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"INVALID_ARGUMENT",
			fmt.Sprintf("unexpected argument %q", arg),
			"ap list does not accept positional arguments.",
			"ap list [--json]",
			[]string{"ap list", "ap list --json"},
		))
	}

	projectRoot, err := deps.getwd()
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"GENERAL_ERROR",
			"failed to determine working directory",
			err.Error(),
			"ap list [--json]",
			nil,
		))
	}

	stages, err := discoverStages(projectRoot)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STAGE_DISCOVERY_FAILED",
			"failed to discover stages",
			err.Error(),
			"ap list [--json]",
			[]string{"ap list"},
		))
	}

	// Merge usage stats from the store (best-effort — missing DB is fine).
	mergeStageStats(projectRoot, deps.store, stages)

	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{"stages": stages}, deps.corrections)
		serialized, err := output.MarshalSuccess(payload)
		if err != nil {
			_, _ = fmt.Fprintln(deps.stderr, err)
			return output.ExitGeneralError
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	_, _ = fmt.Fprint(deps.stdout, renderListHuman(stages))
	return output.ExitSuccess
}

// mergeStageStats loads stage usage statistics from the SQLite store
// and attaches them to the discovered stages. Best-effort: failures are ignored.
func mergeStageStats(projectRoot string, s *store.Store, stages []stageEntry) {
	if s == nil {
		// Try to open the store from the project root.
		dbPath := filepath.Join(projectRoot, ".ap", "ap.db")
		if _, err := os.Stat(dbPath); err != nil {
			return // no database yet
		}
		opened, err := store.Open(dbPath)
		if err != nil {
			return
		}
		defer opened.Close()
		s = opened
	}

	statsMap, err := s.GetStageStats(context.Background())
	if err != nil {
		return
	}

	for i, entry := range stages {
		if st, ok := statsMap[entry.Name]; ok {
			stages[i].Stats = &stageStats{
				TotalSessions:   st.TotalSessions,
				TotalIterations: st.TotalIterations,
				Completed:       st.Completed,
				Failed:          st.Failed,
				AvgIterations:   st.AvgIterations,
				LastRun:         st.LastRun,
			}
		}
	}
}

func discoverStages(projectRoot string) ([]stageEntry, error) {
	builtinDefs, err := stage.LoadBuiltinDefinitions()
	if err != nil {
		return nil, fmt.Errorf("load built-in stages: %w", err)
	}

	merged := make(map[string]stageEntry, len(builtinDefs))
	for name, def := range builtinDefs {
		merged[name] = stageEntry{
			Name:        name,
			Description: descriptionFromDefinition(def),
			Source:      "builtin",
		}
	}

	// Scan project stage directory (overrides builtins).
	localRoot := filepath.Join(projectRoot, ".ap", "stages")
	if err := mergeLocalStages(localRoot, "project", false, merged); err != nil {
		return nil, err
	}

	// Scan user-global stage directory (~/.config/ap/stages/).
	// Does not override project or builtin stages.
	if home, err := os.UserHomeDir(); err == nil {
		userRoot := filepath.Join(home, ".config", "ap", "stages")
		if err := mergeLocalStages(userRoot, "user", true, merged); err != nil {
			return nil, err
		}
	}

	// Sort by name for deterministic output.
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]stageEntry, 0, len(names))
	for _, name := range names {
		result = append(result, merged[name])
	}
	return result, nil
}

func mergeLocalStages(root, source string, skipExisting bool, merged map[string]stageEntry) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read stage root %q: %w", root, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		if skipExisting {
			if _, exists := merged[name]; exists {
				continue
			}
		}
		configPath := filepath.Join(root, name, "stage.yaml")
		config, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s: %w", configPath, err)
		}
		merged[name] = stageEntry{
			Name:        name,
			Description: parseDescription(config),
			Source:      source,
		}
	}
	return nil
}

func descriptionFromDefinition(def stage.Definition) string {
	config, err := def.ReadConfig()
	if err != nil {
		return ""
	}
	return parseDescription(config)
}

func parseDescription(config []byte) string {
	var doc struct {
		Description string `yaml:"description"`
	}
	if err := yaml.NewDecoder(bytes.NewReader(config)).Decode(&doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.Description)
}

func renderListHuman(stages []stageEntry) string {
	if len(stages) == 0 {
		return "No stages found.\n"
	}
	var b strings.Builder
	for i, entry := range stages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(entry.Name)
		if entry.Description != "" {
			b.WriteString("\t")
			b.WriteString(entry.Description)
		}
		if entry.Stats != nil {
			b.WriteString(fmt.Sprintf("  [%d runs, avg %.1f iter, last: %s]",
				entry.Stats.TotalSessions, entry.Stats.AvgIterations,
				truncateTimestamp(entry.Stats.LastRun)))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// truncateTimestamp trims an RFC3339 timestamp to just the date for display.
func truncateTimestamp(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
