package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectModeDecisionTree(t *testing.T) {
	tests := []struct {
		name string
		opts DetectOptions
		want Mode
	}{
		{
			name: "json flag wins",
			opts: DetectOptions{JSONFlag: true, StdoutIsTTY: true, Env: map[string]string{}},
			want: ModeJSON,
		},
		{
			name: "non tty forces json",
			opts: DetectOptions{JSONFlag: false, StdoutIsTTY: false, Env: map[string]string{"AP_OUTPUT": "human"}},
			want: ModeJSON,
		},
		{
			name: "env selects json",
			opts: DetectOptions{JSONFlag: false, StdoutIsTTY: true, Env: map[string]string{"AP_OUTPUT": "json"}},
			want: ModeJSON,
		},
		{
			name: "defaults to human",
			opts: DetectOptions{JSONFlag: false, StdoutIsTTY: true, Env: map[string]string{}},
			want: ModeHuman,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectMode(tc.opts); got != tc.want {
				t.Fatalf("DetectMode()=%q want %q", got, tc.want)
			}
		})
	}
}

func TestNewErrorIncludesRequiredFields(t *testing.T) {
	resp := NewError("stage not found", "stage missing", "closest: ralph", "ap run <spec> <session>", nil)
	if resp.Error.Code != "STAGE_NOT_FOUND" {
		t.Fatalf("code mismatch: %q", resp.Error.Code)
	}
	if resp.Error.Message == "" || resp.Error.Detail == "" || resp.Error.Syntax == "" {
		t.Fatalf("required error fields missing: %+v", resp.Error)
	}
	if resp.Error.Suggestions == nil {
		t.Fatalf("suggestions must be non-nil")
	}
}

func TestMarshalErrorFlattensAvailableMetadata(t *testing.T) {
	resp := NewError("stage_not_found", "oops", "detail", "syntax", []string{"ap list"})
	resp.Error.Available = map[string]any{
		"stages": []string{"ralph", "elegance"},
	}

	raw, err := MarshalError(resp)
	if err != nil {
		t.Fatalf("MarshalError failed: %v", err)
	}

	var payload map[string]map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	errorObj := payload["error"]
	if _, ok := errorObj["available_stages"]; !ok {
		t.Fatalf("expected available_stages metadata, got: %v", errorObj)
	}
}

func TestMarshalSuccessAlwaysIncludesCorrections(t *testing.T) {
	raw, err := MarshalSuccess(NewSuccess(map[string]any{"status": "started"}, nil))
	if err != nil {
		t.Fatalf("MarshalSuccess failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	corrections, ok := payload["corrections"].([]any)
	if !ok {
		t.Fatalf("corrections missing or wrong type: %T", payload["corrections"])
	}
	if len(corrections) != 0 {
		t.Fatalf("expected empty corrections array, got %d entries", len(corrections))
	}
}

func TestRenderNoArgsHumanMatchesContract(t *testing.T) {
	help, err := RenderNoArgs(ModeHuman, "0.1.0")
	if err != nil {
		t.Fatalf("RenderNoArgs failed: %v", err)
	}
	if !strings.Contains(help, "ap - agent pipeline runner") {
		t.Fatalf("missing header in help output: %q", help)
	}
	if !strings.Contains(help, "run <spec> <session>") {
		t.Fatalf("missing run command help")
	}

	if words := len(strings.Fields(help)); words > 100 {
		t.Fatalf("help must stay compact (<=100 tokens), got %d", words)
	}
}

func TestRenderNoArgsJSONIncludesAliasMetadata(t *testing.T) {
	out, err := RenderNoArgs(ModeJSON, "0.1.0")
	if err != nil {
		t.Fatalf("RenderNoArgs failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := payload["command_aliases"].(map[string]any); !ok {
		t.Fatalf("expected command_aliases map, got: %T", payload["command_aliases"])
	}
	if _, ok := payload["commands"].([]any); !ok {
		t.Fatalf("expected commands list, got: %T", payload["commands"])
	}
	if corrections, ok := payload["corrections"].([]any); !ok || len(corrections) != 0 {
		t.Fatalf("expected empty corrections array, got: %#v", payload["corrections"])
	}
}

func TestNormalizeCode(t *testing.T) {
	tests := map[string]string{
		"":                  "GENERAL_ERROR",
		"stage not found":   "STAGE_NOT_FOUND",
		"ProviderFailed":    "PROVIDER_FAILED",
		" provider-timeout": "PROVIDER_TIMEOUT",
	}
	for input, want := range tests {
		if got := NormalizeCode(input); got != want {
			t.Fatalf("NormalizeCode(%q)=%q want %q", input, got, want)
		}
	}
}

// TestExitCodeConstants validates the documented exit code contract.
func TestExitCodeConstants(t *testing.T) {
	codes := map[string]int{
		"ExitSuccess":        ExitSuccess,
		"ExitGeneralError":   ExitGeneralError,
		"ExitInvalidArgs":    ExitInvalidArgs,
		"ExitNotFound":       ExitNotFound,
		"ExitAlreadyExists":  ExitAlreadyExists,
		"ExitLocked":         ExitLocked,
		"ExitProviderError":  ExitProviderError,
		"ExitProviderTimout": ExitProviderTimout,
		"ExitPaused":         ExitPaused,
	}

	expected := map[string]int{
		"ExitSuccess":        0,
		"ExitGeneralError":   1,
		"ExitInvalidArgs":    2,
		"ExitNotFound":       3,
		"ExitAlreadyExists":  4,
		"ExitLocked":         5,
		"ExitProviderError":  10,
		"ExitProviderTimout": 11,
		"ExitPaused":         20,
	}

	for name, code := range codes {
		want, ok := expected[name]
		if !ok {
			t.Errorf("unexpected exit code constant: %s", name)
			continue
		}
		if code != want {
			t.Errorf("%s = %d, want %d", name, code, want)
		}
	}
}

// TestMarshalErrorJSONPayloadShape validates the JSON error envelope.
func TestMarshalErrorJSONPayloadShape(t *testing.T) {
	resp := NewError("INVALID_ARGUMENT", "bad input", "detail here", "ap run <spec>", []string{"ap run ralph sess"})
	raw, err := MarshalError(resp)
	if err != nil {
		t.Fatalf("MarshalError: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Top-level must have exactly "error" key.
	if len(payload) != 1 {
		t.Errorf("expected 1 top-level key, got %d: %v", len(payload), payload)
	}

	errObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object")
	}

	// Required fields.
	required := []string{"code", "message", "suggestions"}
	for _, field := range required {
		if _, ok := errObj[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	// code must be SCREAMING_SNAKE.
	code, _ := errObj["code"].(string)
	if code != "INVALID_ARGUMENT" {
		t.Errorf("code = %q, want INVALID_ARGUMENT", code)
	}

	// suggestions must be an array.
	suggestions, ok := errObj["suggestions"].([]any)
	if !ok {
		t.Errorf("suggestions not an array: %T", errObj["suggestions"])
	}
	if len(suggestions) != 1 {
		t.Errorf("suggestions count = %d, want 1", len(suggestions))
	}
}

// TestMarshalSuccessJSONPayloadShape validates the success envelope.
func TestMarshalSuccessJSONPayloadShape(t *testing.T) {
	resp := NewSuccess(map[string]any{
		"session": "my-session",
		"status":  "completed",
	}, []Correction{{From: "start", To: "run", Hint: "alias"}})

	raw, err := MarshalSuccess(resp)
	if err != nil {
		t.Fatalf("MarshalSuccess: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Payload fields should be present.
	if payload["session"] != "my-session" {
		t.Errorf("session = %v, want my-session", payload["session"])
	}
	if payload["status"] != "completed" {
		t.Errorf("status = %v, want completed", payload["status"])
	}

	// corrections[] must be present with 1 entry.
	corrections, ok := payload["corrections"].([]any)
	if !ok {
		t.Fatalf("corrections missing or wrong type")
	}
	if len(corrections) != 1 {
		t.Fatalf("corrections count = %d, want 1", len(corrections))
	}

	// Correction entry must have from/to fields.
	c, ok := corrections[0].(map[string]any)
	if !ok {
		t.Fatalf("correction entry not an object")
	}
	if c["from"] != "start" || c["to"] != "run" {
		t.Errorf("correction = %v, want from:start to:run", c)
	}
}

// TestCommandsReturnsCanonicalSet validates the canonical command list.
func TestCommandsReturnsCanonicalSet(t *testing.T) {
	cmds := Commands()
	expected := []string{"run", "list", "status", "resume", "kill", "logs", "clean", "watch", "query"}
	if len(cmds) != len(expected) {
		t.Fatalf("commands count = %d, want %d", len(cmds), len(expected))
	}
	for i, cmd := range cmds {
		if cmd != expected[i] {
			t.Errorf("command[%d] = %q, want %q", i, cmd, expected[i])
		}
	}
}

// TestCommandAliasesMapToCanonical validates alias→canonical mapping.
func TestCommandAliasesMapToCanonical(t *testing.T) {
	aliases := CommandAliases()
	cmds := Commands()
	cmdSet := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		cmdSet[c] = true
	}

	for alias, canonical := range aliases {
		if !cmdSet[canonical] {
			t.Errorf("alias %q maps to unknown command %q", alias, canonical)
		}
	}

	// Spot-check key aliases from AGENTS.md.
	checks := map[string]string{
		"start": "run",
		"ls":    "list",
		"stop":  "kill",
		"tail":  "logs",
		"rm":    "clean",
	}
	for alias, want := range checks {
		got, ok := aliases[alias]
		if !ok {
			t.Errorf("missing alias %q", alias)
			continue
		}
		if got != want {
			t.Errorf("alias %q = %q, want %q", alias, got, want)
		}
	}
}
