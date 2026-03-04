package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/hwells4/ap/internal/output"
)

func TestCommandAliasListIncludesCorrection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"ls", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	corrections, ok := result["corrections"].([]any)
	if !ok || len(corrections) == 0 {
		t.Fatalf("expected non-empty corrections, got: %#v", result["corrections"])
	}
	first := corrections[0].(map[string]any)
	if first["from"] != "ls" || first["to"] != "list" {
		t.Fatalf("unexpected correction: %#v", first)
	}
}

func TestCommandTypoListCorrection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	// "listt" has Levenshtein distance 1 from "list" — within threshold for 5-char input.
	code := runWithDeps([]string{"listt", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	corrections, ok := result["corrections"].([]any)
	if !ok || len(corrections) == 0 {
		t.Fatalf("expected typo correction, got: %#v", result["corrections"])
	}
	first := corrections[0].(map[string]any)
	if first["to"] != "list" {
		t.Fatalf("expected correction target list, got: %#v", first)
	}
}

func TestCommandTypoDestructiveIsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"kil", "session", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	errObj, ok := result["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "UNKNOWN_COMMAND" {
		t.Fatalf("error code = %v, want UNKNOWN_COMMAND", errObj["code"])
	}
}

func TestUnknownCommandIncludesAvailableCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
	}

	code := runWithDeps([]string{"wat", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	errObj := result["error"].(map[string]any)
	commands, ok := errObj["available_commands"].([]any)
	if !ok || len(commands) == 0 {
		t.Fatalf("expected available_commands in error, got: %#v", errObj["available_commands"])
	}
}

func TestRunStageTypoRecovered(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralhp", "my-session", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	parsedSpec := result["parsed_spec"].(map[string]any)
	if parsedSpec["name"] != "ralph" {
		t.Fatalf("parsed_spec.name = %v, want ralph", parsedSpec["name"])
	}

	if !hasCorrection(result["corrections"], "ralhp", "ralph") {
		t.Fatalf("expected stage correction in corrections: %#v", result["corrections"])
	}
}

func TestRunStageNotFoundIncludesAvailableStages(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "zzzzzz", "my-session", "--json"}, deps)
	if code != output.ExitInvalidArgs {
		t.Fatalf("exit code = %d, want %d", code, output.ExitInvalidArgs)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	errObj := result["error"].(map[string]any)
	if errObj["code"] != "STAGE_NOT_FOUND" {
		t.Fatalf("error code = %v, want STAGE_NOT_FOUND", errObj["code"])
	}

	stages, ok := errObj["available_stages"].([]any)
	if !ok || len(stages) == 0 {
		t.Fatalf("expected available_stages array, got: %#v", errObj["available_stages"])
	}
	suggestions, ok := errObj["suggestions"].([]any)
	if !ok || len(suggestions) == 0 {
		t.Fatalf("expected non-empty suggestions, got: %#v", errObj["suggestions"])
	}
}

func TestRunSpecSyntaxRecovery(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "25", "my-session", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request := result["request"].(map[string]any)
	if request["spec"] != "ralph:25" {
		t.Fatalf("request.spec = %v, want ralph:25", request["spec"])
	}
	if !hasCorrection(result["corrections"], "ralph 25", "ralph:25") {
		t.Fatalf("expected spec syntax correction, got: %#v", result["corrections"])
	}
}

func TestRunArgumentOrderRecovery(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "my-session", "ralph", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request := result["request"].(map[string]any)
	if request["spec"] != "ralph" || request["session"] != "my-session" {
		t.Fatalf("request = %#v, expected swapped spec/session", request)
	}
	if !hasCorrection(result["corrections"], "my-session ralph", "ralph my-session") {
		t.Fatalf("expected argument order correction, got: %#v", result["corrections"])
	}
}

func TestRunProviderAliasNormalized(t *testing.T) {
	dir := setupStageDir(t)
	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd: func() (string, error) {
			return dir, nil
		},
	}

	code := runWithDeps([]string{"run", "ralph", "my-session", "--provider", "anthropic", "--explain-spec", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	request := result["request"].(map[string]any)
	if request["provider"] != "claude" {
		t.Fatalf("request.provider = %v, want claude", request["provider"])
	}
	if !hasCorrection(result["corrections"], "anthropic", "claude") {
		t.Fatalf("expected provider correction, got: %#v", result["corrections"])
	}
}

func hasCorrection(raw any, from, to string) bool {
	corrections, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, entry := range corrections {
		correction, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if correction["from"] == from && correction["to"] == to {
			return true
		}
	}
	return false
}
