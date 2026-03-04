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
