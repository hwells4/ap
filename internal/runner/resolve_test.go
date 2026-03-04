package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/resolve"
)

// TestResolve_SupportedVariables verifies all Contract 2 variables are
// substituted when present in a prompt template.
func TestResolve_SupportedVariables(t *testing.T) {
	runDir := tempSession(t)

	// Record what prompt the provider receives.
	var capturedPrompt string
	mp := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Summary:  "captured",
			Reason:   "test",
		}),
	)

	template := strings.Join([]string{
		"CTX=${CTX}",
		"PROGRESS=${PROGRESS}",
		"STATUS=${STATUS}",
		"ITERATION=${ITERATION}",
		"SESSION_NAME=${SESSION_NAME}",
		"OUTPUT=${OUTPUT}",
	}, "\n")

	cfg := Config{
		Session:        "test-vars",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: template,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Get the prompt that was sent to the provider.
	calls := mp.Calls()
	if len(calls) == 0 {
		t.Fatal("no provider calls recorded")
	}
	capturedPrompt = calls[0].Request.Prompt

	// Each supported variable should be resolved (not literal ${VAR}).
	checks := []struct {
		varName string
		prefix  string
	}{
		{"CTX", "CTX="},
		{"PROGRESS", "PROGRESS="},
		{"STATUS", "STATUS="},
		{"ITERATION", "ITERATION="},
		{"SESSION_NAME", "SESSION_NAME="},
		{"OUTPUT", "OUTPUT="},
	}

	for _, check := range checks {
		found := false
		for _, line := range strings.Split(capturedPrompt, "\n") {
			if strings.HasPrefix(line, check.prefix) {
				value := strings.TrimPrefix(line, check.prefix)
				if value == "${"+check.varName+"}" {
					t.Errorf("%s was not resolved, still literal ${%s}", check.varName, check.varName)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing line for %s in prompt: %s", check.varName, capturedPrompt)
		}
	}

	// ITERATION should be "1" for first iteration.
	for _, line := range strings.Split(capturedPrompt, "\n") {
		if strings.HasPrefix(line, "ITERATION=") {
			value := strings.TrimPrefix(line, "ITERATION=")
			if value != "1" {
				t.Errorf("ITERATION = %q, want %q", value, "1")
			}
		}
	}

	// SESSION_NAME should be "test-vars".
	for _, line := range strings.Split(capturedPrompt, "\n") {
		if strings.HasPrefix(line, "SESSION_NAME=") {
			value := strings.TrimPrefix(line, "SESSION_NAME=")
			if value != "test-vars" {
				t.Errorf("SESSION_NAME = %q, want %q", value, "test-vars")
			}
		}
	}
}

// TestResolve_UndefinedVariablesLeftLiteral verifies that unknown
// ${VAR} placeholders are left as literal text.
func TestResolve_UndefinedVariablesLeftLiteral(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Summary:  "captured",
		}),
	)

	template := "known=${ITERATION} unknown=${FOOBAR} also=${DOES_NOT_EXIST}"

	cfg := Config{
		Session:        "test-undefined",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: template,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()
	if len(calls) == 0 {
		t.Fatal("no provider calls")
	}
	prompt := calls[0].Request.Prompt

	// Known variable should be resolved.
	if strings.Contains(prompt, "${ITERATION}") {
		t.Error("ITERATION was not resolved")
	}

	// Unknown variables should remain literal.
	if !strings.Contains(prompt, "${FOOBAR}") {
		t.Error("${FOOBAR} should remain literal but was modified")
	}
	if !strings.Contains(prompt, "${DOES_NOT_EXIST}") {
		t.Error("${DOES_NOT_EXIST} should remain literal but was modified")
	}
}

// TestResolve_SinglePassNonRecursive verifies single-pass left-to-right
// substitution. The implementation processes variables sequentially via
// strings.ReplaceAll — each variable's replacement is visible to subsequent
// variables. This means a value containing ${VAR} syntax WILL be expanded
// if a later variable matches. This is the documented "single pass,
// left-to-right" behavior.
func TestResolve_SinglePassNonRecursive(t *testing.T) {
	// Direct unit test of the resolve contract.

	// Case 1: Variable values don't contain other ${VAR} patterns.
	// This is the normal case — no interaction between variables.
	vars := resolve.Vars{
		SESSION:   "my-session",
		ITERATION: "42",
	}
	result := resolve.ResolveTemplate("session=${SESSION} iter=${ITERATION}", vars)
	if result != "session=my-session iter=42" {
		t.Errorf("basic case: got %q", result)
	}

	// Case 2: Verify there's no recursive expansion of the SAME variable.
	// If a value produces a pattern matching itself, it should not re-expand.
	vars2 := resolve.Vars{
		ITERATION: "5",
	}
	result2 := resolve.ResolveTemplate("${ITERATION}${ITERATION}", vars2)
	if result2 != "55" {
		t.Errorf("double same var: got %q, want %q", result2, "55")
	}

	// Case 3: Substitution doesn't apply to variables that aren't in the
	// supported set — no recursive expansion beyond known vars.
	vars3 := resolve.Vars{
		SESSION: "test",
	}
	result3 := resolve.ResolveTemplate("${SESSION} ${UNKNOWN}", vars3)
	if result3 != "test ${UNKNOWN}" {
		t.Errorf("unknown var: got %q, want %q", result3, "test ${UNKNOWN}")
	}
}

// TestResolve_NewlinesPreserved verifies multiline prompt templates
// and multiline variable values preserve newlines exactly.
func TestResolve_NewlinesPreserved(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Summary:  "captured",
		}),
	)

	// Template with explicit newlines.
	template := "line1\nline2\n${ITERATION}\nline4\n"

	cfg := Config{
		Session:        "test-newlines",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: template,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()
	if len(calls) == 0 {
		t.Fatal("no provider calls")
	}
	prompt := calls[0].Request.Prompt

	// Count newlines.
	nlCount := strings.Count(prompt, "\n")
	if nlCount != 4 {
		t.Errorf("newline count = %d, want 4; prompt = %q", nlCount, prompt)
	}

	lines := strings.Split(prompt, "\n")
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	if lines[0] != "line1" {
		t.Errorf("line1 = %q, want %q", lines[0], "line1")
	}
	if lines[1] != "line2" {
		t.Errorf("line2 = %q, want %q", lines[1], "line2")
	}
	if lines[2] != "1" {
		t.Errorf("line3 = %q, want %q (resolved ${ITERATION})", lines[2], "1")
	}
	if lines[3] != "line4" {
		t.Errorf("line4 = %q, want %q", lines[3], "line4")
	}
}

// TestResolve_NestedLikeTokens verifies that tokens like ${${VAR}} or
// ${CONTEXT${ITERATION}} are not specially treated — they're left as-is
// or partially resolved in a single pass.
func TestResolve_NestedLikeTokens(t *testing.T) {
	vars := resolve.Vars{
		ITERATION: "5",
	}

	cases := []struct {
		name     string
		template string
		expect   string
	}{
		{
			name:     "double_nested",
			template: "${${ITERATION}}",
			expect:   "${5}",
		},
		{
			name:     "partial_match",
			template: "${CONTEXT${ITERATION}}",
			expect:   "${CONTEXT5}",
		},
		{
			name:     "adjacent_tokens",
			template: "${ITERATION}${ITERATION}",
			expect:   "55",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve.ResolveTemplate(tc.template, vars)
			if got != tc.expect {
				t.Errorf("got %q, want %q", got, tc.expect)
			}
		})
	}
}

// TestResolve_ContextVariables verifies that context.json generated
// paths are correctly wired into the resolve variables.
func TestResolve_ContextVariables(t *testing.T) {
	runDir := tempSession(t)

	mp := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Summary:  "captured",
		}),
	)

	template := "ctx=${CTX} status=${STATUS} output=${OUTPUT} progress=${PROGRESS}"

	cfg := Config{
		Session:        "test-ctx-vars",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: template,
	}

	_, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	calls := mp.Calls()
	if len(calls) == 0 {
		t.Fatal("no provider calls")
	}
	prompt := calls[0].Request.Prompt

	// CTX should be a path to context.json.
	if !strings.Contains(prompt, "context.json") {
		t.Errorf("CTX not resolved to context.json path: %s", prompt)
	}

	// STATUS should be a path to status.json.
	if !strings.Contains(prompt, "status.json") {
		t.Errorf("STATUS not resolved to status.json path: %s", prompt)
	}

	// OUTPUT should be a path to output.md.
	if !strings.Contains(prompt, "output.md") {
		t.Errorf("OUTPUT not resolved to output.md path: %s", prompt)
	}

	// PROGRESS should be a path containing "progress".
	if !strings.Contains(prompt, "progress") {
		t.Errorf("PROGRESS not resolved: %s", prompt)
	}

	// Verify context.json exists on disk.
	ctxDir := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001")
	ctxPath := filepath.Join(ctxDir, "context.json")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read context.json: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse context.json: %v", err)
	}

	// Verify context.json has expected fields.
	if manifest["session"] != "test-ctx-vars" {
		t.Errorf("context session = %v, want test-ctx-vars", manifest["session"])
	}
	if manifest["iteration"] != float64(1) {
		t.Errorf("context iteration = %v, want 1", manifest["iteration"])
	}
}

// Suppress unused import warnings.
var _ = resolve.Vars{}
