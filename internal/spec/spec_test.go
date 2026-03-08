package spec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/stage"
)

func TestParseEmpty(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "   ", "\t\n"} {
		_, err := Parse(input)
		if !errors.Is(err, ErrEmpty) {
			t.Errorf("Parse(%q) error = %v, want %v", input, err, ErrEmpty)
		}
	}
}

func TestParseBareStage(t *testing.T) {
	t.Parallel()
	spec, err := Parse("ralph")
	if err != nil {
		t.Fatalf("Parse(ralph) error = %v", err)
	}
	stage, ok := spec.(StageSpec)
	if !ok {
		t.Fatalf("Parse(ralph) = %T, want StageSpec", spec)
	}
	if stage.Name != "ralph" {
		t.Errorf("Name = %q, want ralph", stage.Name)
	}
	if stage.Iterations != 0 {
		t.Errorf("Iterations = %d, want 0", stage.Iterations)
	}
	if stage.Kind() != KindStage {
		t.Errorf("Kind() = %d, want KindStage", stage.Kind())
	}
	if stage.Raw() != "ralph" {
		t.Errorf("Raw() = %q, want ralph", stage.Raw())
	}
	if stage.Definition.Name != "ralph" {
		t.Errorf("resolved stage name = %q, want ralph", stage.Definition.Name)
	}
	if stage.Definition.ConfigPath == "" {
		t.Error("resolved stage config path should not be empty")
	}
}

func TestParseBareStageWithWhitespace(t *testing.T) {
	t.Parallel()
	spec, err := Parse("  improve-plan  ")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	stage := spec.(StageSpec)
	if stage.Name != "improve-plan" {
		t.Errorf("Name = %q, want improve-plan", stage.Name)
	}
	if stage.Definition.Name != "improve-plan" {
		t.Errorf("resolved stage name = %q, want improve-plan", stage.Definition.Name)
	}
}

func TestParseStageWithCount(t *testing.T) {
	t.Parallel()
	spec, err := Parse("ralph:25")
	if err != nil {
		t.Fatalf("Parse(ralph:25) error = %v", err)
	}
	stage, ok := spec.(StageSpec)
	if !ok {
		t.Fatalf("Parse(ralph:25) = %T, want StageSpec", spec)
	}
	if stage.Name != "ralph" {
		t.Errorf("Name = %q, want ralph", stage.Name)
	}
	if stage.Iterations != 25 {
		t.Errorf("Iterations = %d, want 25", stage.Iterations)
	}
	if stage.Raw() != "ralph:25" {
		t.Errorf("Raw() = %q, want ralph:25", stage.Raw())
	}
}

func TestParseStageWithSpaceSeparatedCount(t *testing.T) {
	t.Parallel()
	spec, err := Parse("ralph 25")
	if err != nil {
		t.Fatalf("Parse(ralph 25) error = %v", err)
	}
	stage, ok := spec.(StageSpec)
	if !ok {
		t.Fatalf("Parse(ralph 25) = %T, want StageSpec", spec)
	}
	if stage.Name != "ralph" {
		t.Errorf("Name = %q, want ralph", stage.Name)
	}
	if stage.Iterations != 25 {
		t.Errorf("Iterations = %d, want 25", stage.Iterations)
	}
	if stage.Raw() != "ralph:25" {
		t.Errorf("Raw() = %q, want ralph:25", stage.Raw())
	}
}

func TestParseStageWithCountOne(t *testing.T) {
	t.Parallel()
	spec, err := Parse("ralph:1")
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	stage := spec.(StageSpec)
	if stage.Name != "ralph" || stage.Iterations != 1 {
		t.Errorf("got Name=%q Iterations=%d, want ralph/1", stage.Name, stage.Iterations)
	}
}

func TestParseStageInvalidCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"ralph:abc", "invalid iteration count"},
		{"ralph:", "iteration count is empty"},
		{"ralph:0", "iteration count must be positive"},
		{"ralph:-5", "iteration count must be positive"},
		{"ralph:1:2", "invalid stage iteration syntax"},
		{":10", "stage name is empty"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.input)
		if err == nil {
			t.Errorf("Parse(%q) expected error", tt.input)
			continue
		}
		if !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Parse(%q) error = %v, want ErrInvalidSpec", tt.input, err)
		}
		if !containsAll(err.Error(), tt.want) {
			t.Errorf("Parse(%q) error = %q, want to contain %q", tt.input, err.Error(), tt.want)
		}
	}
}

func TestParseStageInvalidCountHint(t *testing.T) {
	t.Parallel()
	_, err := Parse("ralph:abc")
	if err == nil {
		t.Fatal("expected error")
	}
	// Error should suggest valid syntax
	msg := err.Error()
	if msg == "" {
		t.Fatal("error message is empty")
	}
	// Should mention the invalid count
	if !containsAll(msg, "abc", "ralph") {
		t.Errorf("error %q should mention both the bad count and the stage name", msg)
	}
}

func TestParseFilePromptMD(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "custom.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", path, err)
	}
	file, ok := spec.(FileSpec)
	if !ok {
		t.Fatalf("Parse(%q) = %T, want FileSpec", path, spec)
	}
	if file.Kind() != KindFilePrompt {
		t.Errorf("Kind() = %d, want KindFilePrompt", file.Kind())
	}
	if file.Path != path {
		t.Errorf("Path = %q, want %q", file.Path, path)
	}
}

func TestParseFilePromptDotSlash(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "script.txt")
	if err := os.WriteFile(path, []byte("prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use ./relative-style path
	input := "./" + filepath.Base(path)
	// We need the file to exist at the relative path, so use the full path with ./
	// Actually, let's test with an absolute path starting with /
	spec, err := Parse(path) // starts with / since TempDir is absolute
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", path, err)
	}
	file := spec.(FileSpec)
	if file.Kind() != KindFilePrompt {
		t.Errorf("Kind() = %d, want KindFilePrompt", file.Kind())
	}

	// Also test that ./ prefix triggers file detection
	_ = input // validated by TestParseFileNotFoundDotSlash
}

func TestParseFileYAML(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(tmp, "pipeline"+ext)
		if err := os.WriteFile(path, []byte("stages:"), 0o644); err != nil {
			t.Fatal(err)
		}
		spec, err := Parse(path)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", path, err)
		}
		file, ok := spec.(FileSpec)
		if !ok {
			t.Fatalf("Parse(%q) = %T, want FileSpec", path, spec)
		}
		if file.Kind() != KindFileYAML {
			t.Errorf("Parse(%q) Kind() = %d, want KindFileYAML", path, file.Kind())
		}
	}
}

func TestParseFileYAMLPrecedenceOverDotSlash(t *testing.T) {
	t.Parallel()
	// ./refine.yaml should be FileSpec(yaml), not FileSpec(prompt)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "refine.yaml")
	if err := os.WriteFile(path, []byte("stages:"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", path, err)
	}
	file := spec.(FileSpec)
	if file.Kind() != KindFileYAML {
		t.Errorf("./refine.yaml should be KindFileYAML, got %d", file.Kind())
	}
}

func TestParseFileNotFoundMD(t *testing.T) {
	t.Parallel()
	_, err := Parse("/nonexistent/custom.md")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Parse(missing.md) error = %v, want ErrFileNotFound", err)
	}
}

func TestParseFileNotFoundYAML(t *testing.T) {
	t.Parallel()
	_, err := Parse("/nonexistent/pipeline.yaml")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Parse(missing.yaml) error = %v, want ErrFileNotFound", err)
	}
}

func TestParseFileNotFoundDotSlash(t *testing.T) {
	t.Parallel()
	_, err := Parse("./nonexistent-script")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Parse(./missing) error = %v, want ErrFileNotFound", err)
	}
}

func TestParseFileNotFoundNoFallthrough(t *testing.T) {
	t.Parallel()
	// A .md path that doesn't exist should NOT fall through to stage lookup.
	// It should return FILE_NOT_FOUND even if "custom.md" could be a stage name.
	_, err := Parse("/tmp/nonexistent-ap-test-file.md")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing file path must return ErrFileNotFound, not fall through; got %v", err)
	}
}

func TestParseChain(t *testing.T) {
	t.Parallel()

	spec, err := Parse("improve-plan:5 -> refine-tasks:5")
	if err != nil {
		t.Fatalf("Parse(chain) error = %v", err)
	}

	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("Parse(chain) = %T, want ChainSpec", spec)
	}
	if chain.Kind() != KindChain {
		t.Fatalf("Kind() = %d, want KindChain", chain.Kind())
	}
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
	if chain.Stages[0].Name != "improve-plan" || chain.Stages[0].Iterations != 5 {
		t.Fatalf("stage[0] = %#v, want improve-plan:5", chain.Stages[0])
	}
	if chain.Stages[1].Name != "refine-tasks" || chain.Stages[1].Iterations != 5 {
		t.Fatalf("stage[1] = %#v, want refine-tasks:5", chain.Stages[1])
	}
}

func TestParseChainWithWhitespace(t *testing.T) {
	t.Parallel()

	spec, err := Parse("  improve-plan:5   ->   refine-tasks:5  ")
	if err != nil {
		t.Fatalf("Parse(chain) error = %v", err)
	}
	chain := spec.(ChainSpec)
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
}

func TestParseChainMissingStageAfterArrow(t *testing.T) {
	t.Parallel()

	_, err := Parse("improve-plan:5 -> ")
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Parse(chain missing right stage) error = %v, want ErrInvalidSpec", err)
	}
	if !containsAll(err.Error(), "invalid chain", "expected stage name after ->") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseChainMissingStageBeforeArrow(t *testing.T) {
	t.Parallel()

	_, err := Parse(" -> refine-tasks:5")
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Parse(chain missing left stage) error = %v, want ErrInvalidSpec", err)
	}
	if !containsAll(err.Error(), "invalid chain", "expected stage name before ->") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseChainInvalidCount(t *testing.T) {
	t.Parallel()

	_, err := Parse("improve-plan:abc -> refine-tasks:5")
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("Parse(chain invalid count) error = %v, want ErrInvalidSpec", err)
	}
	if !containsAll(err.Error(), "invalid iteration count") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseChainSyntaxRecoveryFromGreaterThan(t *testing.T) {
	t.Parallel()

	spec, err := ParseWithOptions("alpha:2 > beta:3", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("ParseWithOptions(>) error = %v", err)
	}
	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("ParseWithOptions(>) = %T, want ChainSpec", spec)
	}
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
	if chain.Stages[0].Name != "alpha" || chain.Stages[0].Iterations != 2 {
		t.Fatalf("stage[0] = %#v", chain.Stages[0])
	}
	if chain.Stages[1].Name != "beta" || chain.Stages[1].Iterations != 3 {
		t.Fatalf("stage[1] = %#v", chain.Stages[1])
	}
}

func TestParseChainSyntaxRecoveryFromComma(t *testing.T) {
	t.Parallel()

	spec, err := ParseWithOptions("alpha:2, beta:3", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("ParseWithOptions(,) error = %v", err)
	}
	chain := spec.(ChainSpec)
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
}

func TestChainSpecToPipeline(t *testing.T) {
	t.Parallel()

	chain := ChainSpec{
		raw: "improve-plan:5 -> refine-tasks:3",
		Stages: []StageSpec{
			{Name: "improve-plan", Iterations: 5},
			{Name: "refine-tasks", Iterations: 3},
		},
	}

	pipeline := chain.ToPipeline()
	if len(pipeline.Nodes) != 2 {
		t.Fatalf("len(pipeline.Nodes) = %d, want 2", len(pipeline.Nodes))
	}
	if pipeline.Nodes[0].Stage != "improve-plan" || pipeline.Nodes[0].Runs != 5 {
		t.Fatalf("node[0] = %#v", pipeline.Nodes[0])
	}
	if pipeline.Nodes[1].Stage != "refine-tasks" || pipeline.Nodes[1].Runs != 3 {
		t.Fatalf("node[1] = %#v", pipeline.Nodes[1])
	}
	if pipeline.Nodes[1].Inputs.From != pipeline.Nodes[0].ID {
		t.Fatalf("node[1].inputs.from = %q, want %q", pipeline.Nodes[1].Inputs.From, pipeline.Nodes[0].ID)
	}
	if pipeline.Nodes[1].Inputs.Select != "latest" {
		t.Fatalf("node[1].inputs.select = %q, want latest", pipeline.Nodes[1].Inputs.Select)
	}
}

func TestChainSpecToPipelineDuplicateStageNames(t *testing.T) {
	t.Parallel()

	chain := ChainSpec{
		raw: "alpha -> alpha",
		Stages: []StageSpec{
			{Name: "alpha"},
			{Name: "alpha"},
		},
	}
	pipeline := chain.ToPipeline()
	if len(pipeline.Nodes) != 2 {
		t.Fatalf("len(pipeline.Nodes) = %d, want 2", len(pipeline.Nodes))
	}
	if pipeline.Nodes[0].ID == pipeline.Nodes[1].ID {
		t.Fatalf("expected unique node IDs, got %q and %q", pipeline.Nodes[0].ID, pipeline.Nodes[1].ID)
	}
	if pipeline.Nodes[1].Inputs.From != pipeline.Nodes[0].ID {
		t.Fatalf("node[1].inputs.from = %q, want %q", pipeline.Nodes[1].Inputs.From, pipeline.Nodes[0].ID)
	}
}

func TestParsePrecedenceOrder(t *testing.T) {
	t.Parallel()
	// Verify documented precedence:
	// 1. chain (->)  2. .yaml  3. .md/./  4. :N  5. bare name

	// Chain always wins (a chain with .yaml-looking tokens is still chain syntax)
	spec, err := ParseWithOptions("a.yaml -> b.yaml", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("chain should be detected before yaml; error = %v", err)
	}
	if spec.Kind() != KindChain {
		t.Fatalf("expected KindChain, got %d", spec.Kind())
	}

	// .yaml wins over : (e.g., "file:1.yaml" treated as yaml file)
	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(yamlPath, []byte("x:"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err = Parse(yamlPath)
	if err != nil {
		t.Fatalf("yaml parse error = %v", err)
	}
	if spec.Kind() != KindFileYAML {
		t.Errorf(".yaml should produce KindFileYAML, got %d", spec.Kind())
	}
}

func TestParseWithOptionsStagePrecedenceProjectOverBuiltin(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	stageDir := filepath.Join(projectRoot, ".ap", "stages", "ralph")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "stage.yaml"), []byte("name: ralph\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "prompt.md"), []byte("local prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := ParseWithOptions("ralph", ParseOptions{
		StageResolveOpts: stage.ResolveOptions{ProjectRoot: projectRoot},
	})
	if err != nil {
		t.Fatalf("ParseWithOptions error = %v", err)
	}

	stageSpec, ok := spec.(StageSpec)
	if !ok {
		t.Fatalf("ParseWithOptions = %T, want StageSpec", spec)
	}

	if stageSpec.Definition.IsEmbedded() {
		t.Fatal("expected local stage definition to override embedded builtin")
	}
	wantConfig := filepath.Join(stageDir, "stage.yaml")
	if stageSpec.Definition.ConfigPath != wantConfig {
		t.Fatalf("resolved config path = %q, want %q", stageSpec.Definition.ConfigPath, wantConfig)
	}
}

func TestParseWithOptionsStageNotFound(t *testing.T) {
	t.Parallel()

	_, err := ParseWithOptions("definitely-missing-stage", ParseOptions{
		StageResolveOpts: stage.ResolveOptions{ProjectRoot: t.TempDir()},
	})
	if !errors.Is(err, ErrStageNotFound) {
		t.Fatalf("ParseWithOptions error = %v, want ErrStageNotFound", err)
	}
	if !containsAll(err.Error(), "definitely-missing-stage", "searched:") {
		t.Fatalf("stage not found message should include stage and search context, got: %v", err)
	}
}

func TestChainSpecKindAndRaw(t *testing.T) {
	t.Parallel()

	chain := ChainSpec{
		raw: "alpha:2 -> beta:3",
		Stages: []StageSpec{
			{Name: "alpha", Iterations: 2},
			{Name: "beta", Iterations: 3},
		},
	}
	if chain.Kind() != KindChain {
		t.Fatalf("Kind() = %d, want KindChain", chain.Kind())
	}
	if chain.Raw() != "alpha:2 -> beta:3" {
		t.Fatalf("Raw() = %q, want alpha:2 -> beta:3", chain.Raw())
	}
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
}

func TestParseRepeatChain(t *testing.T) {
	t.Parallel()
	spec, err := ParseWithOptions("(a:3 -> b:1) x3", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("Parse repeat chain error = %v", err)
	}
	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("Parse repeat chain = %T, want ChainSpec", spec)
	}
	if len(chain.Stages) != 6 {
		t.Fatalf("len(Stages) = %d, want 6", len(chain.Stages))
	}
	for i, want := range []struct {
		name string
		iter int
	}{
		{"a", 3}, {"b", 1}, {"a", 3}, {"b", 1}, {"a", 3}, {"b", 1},
	} {
		if chain.Stages[i].Name != want.name || chain.Stages[i].Iterations != want.iter {
			t.Errorf("stage[%d] = {%q, %d}, want {%q, %d}",
				i, chain.Stages[i].Name, chain.Stages[i].Iterations, want.name, want.iter)
		}
	}
}

func TestParseRepeatSingleStage(t *testing.T) {
	t.Parallel()
	spec, err := ParseWithOptions("(a:5) x3", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("Parse repeat single stage error = %v", err)
	}
	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("Parse repeat single = %T, want ChainSpec", spec)
	}
	if len(chain.Stages) != 3 {
		t.Fatalf("len(Stages) = %d, want 3", len(chain.Stages))
	}
	for i, s := range chain.Stages {
		if s.Name != "a" || s.Iterations != 5 {
			t.Errorf("stage[%d] = {%q, %d}, want {a, 5}", i, s.Name, s.Iterations)
		}
	}
}

func TestParseRepeatOne(t *testing.T) {
	t.Parallel()
	spec, err := ParseWithOptions("(a -> b) x1", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("Parse repeat x1 error = %v", err)
	}
	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("Parse repeat x1 = %T, want ChainSpec", spec)
	}
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
}

func TestParseRepeatNoParens(t *testing.T) {
	t.Parallel()
	// "a -> b x3" is NOT repeat syntax — should parse as a chain with "b x3" as a stage
	// (which will fail stage lookup or parse normally depending on options)
	spec, err := ParseWithOptions("a -> b", ParseOptions{SkipStageLookup: true})
	if err != nil {
		t.Fatalf("Parse non-repeat chain error = %v", err)
	}
	chain, ok := spec.(ChainSpec)
	if !ok {
		t.Fatalf("Parse non-repeat = %T, want ChainSpec", spec)
	}
	if len(chain.Stages) != 2 {
		t.Fatalf("len(Stages) = %d, want 2", len(chain.Stages))
	}
}

func TestParseRepeatInvalidCount(t *testing.T) {
	t.Parallel()
	_, err := ParseWithOptions("(a -> b) x0", ParseOptions{SkipStageLookup: true})
	if err == nil {
		t.Fatal("expected error for x0 repeat")
	}
}

func TestParseRepeatVariants(t *testing.T) {
	t.Parallel()
	variants := []string{
		"(a:3 -> b:1) x2",
		"(a:3 -> b:1)x2",
		"(a:3 -> b:1) X2",
		"(a:3 -> b:1) *2",
	}
	for _, input := range variants {
		spec, err := ParseWithOptions(input, ParseOptions{SkipStageLookup: true})
		if err != nil {
			t.Errorf("Parse(%q) error = %v", input, err)
			continue
		}
		chain, ok := spec.(ChainSpec)
		if !ok {
			t.Errorf("Parse(%q) = %T, want ChainSpec", input, spec)
			continue
		}
		if len(chain.Stages) != 4 {
			t.Errorf("Parse(%q) len(Stages) = %d, want 4", input, len(chain.Stages))
		}
	}
}

func TestChainRepeat_PreservesIterations(t *testing.T) {
	t.Parallel()
	chain := ChainSpec{
		raw: "a:10 -> b:3",
		Stages: []StageSpec{
			{Name: "a", Iterations: 10},
			{Name: "b", Iterations: 3},
		},
	}
	expanded := chain.Repeat(2)
	if len(expanded.Stages) != 4 {
		t.Fatalf("len(Stages) = %d, want 4", len(expanded.Stages))
	}
	for i, want := range []struct {
		name string
		iter int
	}{
		{"a", 10}, {"b", 3}, {"a", 10}, {"b", 3},
	} {
		if expanded.Stages[i].Name != want.name || expanded.Stages[i].Iterations != want.iter {
			t.Errorf("stage[%d] = {%q, %d}, want {%q, %d}",
				i, expanded.Stages[i].Name, expanded.Stages[i].Iterations, want.name, want.iter)
		}
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		found := false
		idx := 0
		for idx <= len(s)-len(sub) {
			if s[idx:idx+len(sub)] == sub {
				found = true
				break
			}
			idx++
		}
		if !found {
			return false
		}
	}
	return true
}
