package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeManifest creates a manifest.json for testing from_parallel resolution.
func writeManifest(t *testing.T, dir string, blockName string, providers map[string]map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := map[string]any{
		"block": map[string]any{
			"name": blockName,
		},
		"providers": providers,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestFromParallel_ShortForm_AllProviders(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create manifest with two providers.
	blockDir := filepath.Join(tmpDir, "parallel-00-iterate")
	manifestPath := writeManifest(t, blockDir, "iterate", map[string]map[string]any{
		"claude": {
			"analyze": map[string]any{
				"latest_output":      "/tmp/claude/output.md",
				"status":             "completed",
				"iterations":         3,
				"termination_reason": "fixed",
			},
		},
		"codex": {
			"analyze": map[string]any{
				"latest_output":      "/tmp/codex/output.md",
				"status":             "completed",
				"iterations":         2,
				"termination_reason": "stopped",
			},
		},
	})

	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		ParallelBlocks: map[string]ParallelBlock{
			"iterate": {ManifestPath: manifestPath},
		},
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`"analyze"`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	if len(inputs.FromParallel) != 1 {
		t.Fatalf("from_parallel length = %d, want 1", len(inputs.FromParallel))
	}

	entry := inputs.FromParallel[0]
	if entry["stage"] != "analyze" {
		t.Fatalf("stage = %v, want analyze", entry["stage"])
	}
	if entry["block"] != "iterate" {
		t.Fatalf("block = %v, want iterate", entry["block"])
	}

	providers, ok := entry["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers is not a map: %T", entry["providers"])
	}
	if len(providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(providers))
	}

	claude, ok := providers["claude"].(map[string]any)
	if !ok {
		t.Fatalf("claude is not a map: %T", providers["claude"])
	}
	if claude["output"] != "/tmp/claude/output.md" {
		t.Fatalf("claude output = %v, want /tmp/claude/output.md", claude["output"])
	}
	if claude["status"] != "completed" {
		t.Fatalf("claude status = %v, want completed", claude["status"])
	}
}

func TestFromParallel_FullForm_ProviderFilter(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	blockDir := filepath.Join(tmpDir, "parallel-00-iterate")
	manifestPath := writeManifest(t, blockDir, "iterate", map[string]map[string]any{
		"claude": {
			"analyze": map[string]any{
				"latest_output": "/tmp/claude/output.md",
				"status":        "completed",
				"iterations":    3,
			},
		},
		"codex": {
			"analyze": map[string]any{
				"latest_output": "/tmp/codex/output.md",
				"status":        "completed",
				"iterations":    2,
			},
		},
		"gemini": {
			"analyze": map[string]any{
				"latest_output": "/tmp/gemini/output.md",
				"status":        "completed",
				"iterations":    1,
			},
		},
	})

	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		ParallelBlocks: map[string]ParallelBlock{
			"iterate": {ManifestPath: manifestPath},
		},
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`{"stage":"analyze","providers":["claude","gemini"]}`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	if len(inputs.FromParallel) != 1 {
		t.Fatalf("from_parallel length = %d, want 1", len(inputs.FromParallel))
	}

	providers, ok := inputs.FromParallel[0]["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers is not a map: %T", inputs.FromParallel[0]["providers"])
	}

	// Only claude and gemini should be included, not codex.
	if len(providers) != 2 {
		t.Fatalf("providers count = %d, want 2 (filtered)", len(providers))
	}
	if _, ok := providers["claude"]; !ok {
		t.Fatal("claude should be in providers")
	}
	if _, ok := providers["gemini"]; !ok {
		t.Fatal("gemini should be in providers")
	}
	if _, ok := providers["codex"]; ok {
		t.Fatal("codex should NOT be in providers (filtered out)")
	}
}

func TestFromParallel_SelectHistory(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	blockDir := filepath.Join(tmpDir, "parallel-00-iterate")
	manifestPath := writeManifest(t, blockDir, "iterate", map[string]map[string]any{
		"claude": {
			"plan": map[string]any{
				"latest_output":      "/tmp/plan/003.md",
				"status":             "completed",
				"iterations":         3,
				"termination_reason": "fixed",
				"history":            []string{"/tmp/plan/001.md", "/tmp/plan/002.md", "/tmp/plan/003.md"},
			},
		},
	})

	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		ParallelBlocks: map[string]ParallelBlock{
			"iterate": {ManifestPath: manifestPath},
		},
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`{"stage":"plan","select":"history"}`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	providers, ok := inputs.FromParallel[0]["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %T", inputs.FromParallel[0]["providers"])
	}
	claude, ok := providers["claude"].(map[string]any)
	if !ok {
		t.Fatalf("claude not a map: %T", providers["claude"])
	}

	history, ok := claude["history"].([]string)
	if !ok {
		// JSON unmarshal creates []any, not []string.
		historyAny, ok := claude["history"].([]any)
		if !ok {
			t.Fatalf("history is not a slice: %T", claude["history"])
		}
		if len(historyAny) != 3 {
			t.Fatalf("history length = %d, want 3", len(historyAny))
		}
	} else {
		if len(history) != 3 {
			t.Fatalf("history length = %d, want 3", len(history))
		}
	}

	if inputs.FromParallel[0]["select"] != "history" {
		t.Fatalf("select = %v, want history", inputs.FromParallel[0]["select"])
	}
}

func TestFromParallel_SelectLatest_OmitsHistory(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	blockDir := filepath.Join(tmpDir, "parallel-00-iterate")
	manifestPath := writeManifest(t, blockDir, "iterate", map[string]map[string]any{
		"claude": {
			"plan": map[string]any{
				"latest_output": "/tmp/plan/003.md",
				"status":        "completed",
				"iterations":    3,
				"history":       []string{"/tmp/plan/001.md", "/tmp/plan/002.md", "/tmp/plan/003.md"},
			},
		},
	})

	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		ParallelBlocks: map[string]ParallelBlock{
			"iterate": {ManifestPath: manifestPath},
		},
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`{"stage":"plan","select":"latest"}`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	providers, ok := inputs.FromParallel[0]["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %T", inputs.FromParallel[0]["providers"])
	}
	claude, ok := providers["claude"].(map[string]any)
	if !ok {
		t.Fatalf("claude not a map: %T", providers["claude"])
	}

	// With select=latest (default), history should NOT be included.
	if _, hasHistory := claude["history"]; hasHistory {
		t.Fatal("history should not be included with select=latest")
	}
}

func TestFromParallel_NoManifest_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`"analyze"`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	// from_parallel should still appear but with empty providers.
	if len(inputs.FromParallel) != 1 {
		t.Fatalf("from_parallel length = %d, want 1", len(inputs.FromParallel))
	}
}

func TestFromParallel_ManifestDiscovery(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Create manifest in parallel-00-iterate/ without explicit config.
	blockDir := filepath.Join(tmpDir, "parallel-00-iterate")
	writeManifest(t, blockDir, "iterate", map[string]map[string]any{
		"claude": {
			"analyze": map[string]any{
				"latest_output": "/tmp/analyze.md",
				"status":        "completed",
				"iterations":    1,
			},
		},
	})

	// No ParallelBlocks config — manifest should be discovered from filesystem.
	stageConfig := StageConfig{
		ID:    "synthesize",
		Index: intPtr(1),
		Inputs: &InputsConfig{
			FromParallel: json.RawMessage(`"analyze"`),
		},
	}

	inputs, err := BuildInputs(tmpDir, stageConfig, 1)
	if err != nil {
		t.Fatalf("BuildInputs: %v", err)
	}

	if len(inputs.FromParallel) != 1 {
		t.Fatalf("from_parallel length = %d, want 1", len(inputs.FromParallel))
	}

	providers, ok := inputs.FromParallel[0]["providers"].(map[string]any)
	if !ok {
		t.Fatalf("providers not a map: %T", inputs.FromParallel[0]["providers"])
	}
	if _, ok := providers["claude"]; !ok {
		t.Fatal("claude should be discovered from manifest")
	}
}
