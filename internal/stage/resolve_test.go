package stage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStagePrecedence(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	pipelineDir := filepath.Join(tempDir, "pipeline")
	agentRoot := filepath.Join(tempDir, "agent")

	projectStages := filepath.Join(projectRoot, ".claude", "stages")
	pipelineStages := filepath.Join(pipelineDir, "stages")
	agentStages := filepath.Join(agentRoot, "scripts", "stages")

	projectStageDir := writeStage(t, projectStages, "alpha", "", "prompt.md")
	writeStage(t, pipelineStages, "alpha", "", "prompt.md")
	writeStage(t, agentStages, "alpha", "", "prompt.md")

	def, err := ResolveStage("alpha", ResolveOptions{
		ProjectRoot:        projectRoot,
		PipelineDir:        pipelineDir,
		AgentPipelinesRoot: agentRoot,
	})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	wantConfig := filepath.Join(projectStageDir, "stage.yaml")
	if def.ConfigPath != wantConfig {
		t.Fatalf("ConfigPath mismatch: got %q want %q", def.ConfigPath, wantConfig)
	}
	wantPrompt := filepath.Join(projectStageDir, "prompt.md")
	if def.PromptPath != wantPrompt {
		t.Fatalf("PromptPath mismatch: got %q want %q", def.PromptPath, wantPrompt)
	}
	if def.Dir != projectStageDir {
		t.Fatalf("Dir mismatch: got %q want %q", def.Dir, projectStageDir)
	}
}

func TestResolveStagePromptField(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	projectStages := filepath.Join(projectRoot, ".claude", "stages")

	stageDir := writeStage(t, projectStages, "alpha", "prompts/custom.md", "prompts/custom.md")
	writeFile(t, filepath.Join(stageDir, "prompt.md"))

	def, err := ResolveStage("alpha", ResolveOptions{ProjectRoot: projectRoot})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	wantPrompt := filepath.Join(stageDir, "prompts", "custom.md")
	if def.PromptPath != wantPrompt {
		t.Fatalf("PromptPath mismatch: got %q want %q", def.PromptPath, wantPrompt)
	}
}

func TestResolveStageMissingPrompt(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	projectStages := filepath.Join(projectRoot, ".claude", "stages")

	writeStage(t, projectStages, "alpha", "", "")

	_, err := ResolveStage("alpha", ResolveOptions{ProjectRoot: projectRoot})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
	if !strings.Contains(err.Error(), "stage alpha has no prompt.md or prompt: field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveStageNotFoundListsPaths(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	pipelineDir := filepath.Join(tempDir, "pipeline")
	agentRoot := filepath.Join(tempDir, "agent")

	_, err := ResolveStage("missing", ResolveOptions{
		ProjectRoot:        projectRoot,
		PipelineDir:        pipelineDir,
		AgentPipelinesRoot: agentRoot,
	})
	if err == nil {
		t.Fatal("expected error for missing stage")
	}

	expectedPaths := []string{
		filepath.Join(projectRoot, ".claude", "stages", "missing", "stage.yaml"),
		filepath.Join(pipelineDir, "stages", "missing", "stage.yaml"),
		filepath.Join(agentRoot, "scripts", "stages", "missing", "stage.yaml"),
	}
	for _, path := range expectedPaths {
		if !strings.Contains(err.Error(), path) {
			t.Fatalf("error missing path %q: %v", path, err)
		}
	}
}

func TestResolveStageFallsBackToEmbeddedBuiltin(t *testing.T) {
	builtins, err := LoadBuiltinDefinitions()
	if err != nil {
		t.Fatalf("LoadBuiltinDefinitions: %v", err)
	}

	def, err := ResolveStage("ralph", ResolveOptions{BuiltinDefinitions: builtins})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	if !def.IsEmbedded() {
		t.Fatalf("expected embedded definition for ralph")
	}
	if !strings.HasPrefix(def.ConfigPath, embeddedPathPrefix) {
		t.Fatalf("expected embedded config path, got %q", def.ConfigPath)
	}

	prompt, err := def.ReadPrompt()
	if err != nil {
		t.Fatalf("ReadPrompt: %v", err)
	}
	if len(bytes.TrimSpace(prompt)) == 0 {
		t.Fatal("expected non-empty embedded prompt")
	}
}

func TestResolveStageUsesEmbeddedBuiltinsByDefault(t *testing.T) {
	def, err := ResolveStage("ralph", ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}
	if !def.IsEmbedded() {
		t.Fatalf("expected embedded definition when no local stage is provided")
	}
}

func TestResolveStageLocalOverridesBuiltin(t *testing.T) {
	tempDir := t.TempDir()
	projectRoot := filepath.Join(tempDir, "project")
	projectStages := filepath.Join(projectRoot, ".claude", "stages")

	localStageDir := writeStage(t, projectStages, "ralph", "", "prompt.md")

	builtins, err := LoadBuiltinDefinitions()
	if err != nil {
		t.Fatalf("LoadBuiltinDefinitions: %v", err)
	}

	def, err := ResolveStage("ralph", ResolveOptions{
		ProjectRoot:        projectRoot,
		BuiltinDefinitions: builtins,
	})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	if def.IsEmbedded() {
		t.Fatalf("expected local stage to override embedded builtin")
	}

	wantConfig := filepath.Join(localStageDir, "stage.yaml")
	if def.ConfigPath != wantConfig {
		t.Fatalf("ConfigPath mismatch: got %q want %q", def.ConfigPath, wantConfig)
	}
}

func TestBuiltinStageNames(t *testing.T) {
	names, err := BuiltinStageNames()
	if err != nil {
		t.Fatalf("BuiltinStageNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected built-in stage names")
	}
	if !contains(names, "ralph") {
		t.Fatalf("expected built-ins to contain ralph, got %v", names)
	}
}

func writeStage(t *testing.T, stagesRoot, name, promptField, promptFile string) string {
	t.Helper()

	stageDir := filepath.Join(stagesRoot, name)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatalf("mkdir stage dir: %v", err)
	}

	var builder strings.Builder
	builder.WriteString("name: ")
	builder.WriteString(name)
	builder.WriteString("\n")
	if promptField != "" {
		builder.WriteString("prompt: ")
		builder.WriteString(promptField)
		builder.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write stage.yaml: %v", err)
	}

	if promptFile != "" {
		writeFile(t, filepath.Join(stageDir, promptFile))
	}

	return stageDir
}

func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
}

func TestReadHooks_Present(t *testing.T) {
	dir := t.TempDir()
	stageDir := filepath.Join(dir, ".claude", "stages", "test")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	config := `name: test
hooks:
  pre_session: "git checkout -b work"
  post_iteration: "git add -A && git commit -m 'iter'"
`
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write stage.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write prompt.md: %v", err)
	}

	def, err := ResolveStage("test", ResolveOptions{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	hooks := def.ReadHooks()
	if hooks == nil {
		t.Fatal("expected hooks to be non-nil")
	}
	if hooks["pre_session"] != "git checkout -b work" {
		t.Fatalf("pre_session = %q, want %q", hooks["pre_session"], "git checkout -b work")
	}
	if hooks["post_iteration"] != "git add -A && git commit -m 'iter'" {
		t.Fatalf("post_iteration = %q", hooks["post_iteration"])
	}
	if hooks["post_session"] != "" {
		t.Fatalf("post_session should be empty, got %q", hooks["post_session"])
	}
}

func TestReadHooks_Absent(t *testing.T) {
	dir := t.TempDir()
	stageDir := filepath.Join(dir, ".claude", "stages", "nohooks")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	config := "name: nohooks\n"
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write stage.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write prompt.md: %v", err)
	}

	def, err := ResolveStage("nohooks", ResolveOptions{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ResolveStage: %v", err)
	}

	hooks := def.ReadHooks()
	if len(hooks) != 0 {
		t.Fatalf("expected no hooks, got %v", hooks)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
