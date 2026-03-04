// Package output implements robot-mode output contracts.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// Mode is the output mode for command rendering.
type Mode string

const (
	// ModeHuman prints compact human-readable output.
	ModeHuman Mode = "human"
	// ModeJSON prints machine-parseable JSON output.
	ModeJSON Mode = "json"
)

// Standard CLI exit codes.
const (
	ExitSuccess        = 0
	ExitGeneralError   = 1
	ExitInvalidArgs    = 2
	ExitNotFound       = 3
	ExitAlreadyExists  = 4
	ExitLocked         = 5
	ExitProviderError  = 10
	ExitProviderTimout = 11
	ExitPaused         = 20
)

// DetectOptions controls output mode detection.
type DetectOptions struct {
	JSONFlag    bool
	StdoutIsTTY bool
	Env         map[string]string
}

// DetectMode applies the documented decision tree:
// 1) --json flag
// 2) stdout non-TTY
// 3) AP_OUTPUT=json
// 4) human mode
func DetectMode(opts DetectOptions) Mode {
	if opts.JSONFlag {
		return ModeJSON
	}
	if !opts.StdoutIsTTY {
		return ModeJSON
	}
	if strings.EqualFold(strings.TrimSpace(opts.Env["AP_OUTPUT"]), "json") {
		return ModeJSON
	}
	return ModeHuman
}

// IsTerminalStdout reports whether stdout is a terminal.
func IsTerminalStdout() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Correction captures a fuzzy normalization performed on a successful command.
type Correction struct {
	From string `json:"from"`
	To   string `json:"to"`
	Hint string `json:"hint,omitempty"`
}

// ErrorPayload is the structured error contract for JSON mode.
type ErrorPayload struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Detail      string         `json:"detail,omitempty"`
	Syntax      string         `json:"syntax,omitempty"`
	Suggestions []string       `json:"suggestions"`
	Available   map[string]any `json:"available,omitempty"`
}

// ErrorResponse wraps a structured error payload.
type ErrorResponse struct {
	Error ErrorPayload `json:"error"`
}

// SuccessResponse is the structured success payload.
// Corrections is always emitted, even when empty.
type SuccessResponse struct {
	Corrections []Correction `json:"corrections"`
	Payload     map[string]any
}

// NoArgsResponse is returned for `ap` with no args in JSON mode.
type NoArgsResponse struct {
	Version        string            `json:"version"`
	Commands       []string          `json:"commands"`
	CommandAliases map[string]string `json:"command_aliases"`
	Corrections    []Correction      `json:"corrections"`
}

// CommandAliases returns known command synonyms for agents.
func CommandAliases() map[string]string {
	return map[string]string{
		"abort":     "kill",
		"cancel":    "kill",
		"check":     "status",
		"continue":  "resume",
		"delete":    "clean",
		"exec":      "run",
		"execute":   "run",
		"follow":    "logs",
		"info":      "status",
		"launch":    "run",
		"ls":        "list",
		"pipelines": "list",
		"prune":     "clean",
		"remove":    "clean",
		"restart":   "resume",
		"rm":        "clean",
		"show":      "list",
		"start":     "run",
		"state":     "status",
		"stages":    "list",
		"stop":      "kill",
		"tail":      "logs",
		"terminate": "kill",
		"watch":     "logs",
	}
}

// Commands returns canonical command names.
func Commands() []string {
	return []string{"run", "list", "status", "resume", "kill", "logs", "clean", "watch"}
}

// NewError builds a structured error payload with normalized code and suggestions.
func NewError(code, message, detail, syntax string, suggestions []string) ErrorResponse {
	if suggestions == nil {
		suggestions = []string{}
	}
	return ErrorResponse{
		Error: ErrorPayload{
			Code:        NormalizeCode(code),
			Message:     strings.TrimSpace(message),
			Detail:      strings.TrimSpace(detail),
			Syntax:      strings.TrimSpace(syntax),
			Suggestions: suggestions,
		},
	}
}

// NewSuccess builds a success payload with guaranteed corrections[].
func NewSuccess(payload map[string]any, corrections []Correction) SuccessResponse {
	if payload == nil {
		payload = map[string]any{}
	}
	if corrections == nil {
		corrections = []Correction{}
	}
	return SuccessResponse{
		Payload:     payload,
		Corrections: corrections,
	}
}

// NewNoArgsResponse returns the JSON payload for `ap` with no args.
func NewNoArgsResponse(version string) NoArgsResponse {
	aliases := CommandAliases()
	return NoArgsResponse{
		Version:        version,
		Commands:       Commands(),
		CommandAliases: aliases,
		Corrections:    []Correction{},
	}
}

// RenderNoArgs renders no-args help text/payload for the selected mode.
func RenderNoArgs(mode Mode, version string) (string, error) {
	if mode == ModeJSON {
		payload, err := json.Marshal(NewNoArgsResponse(version))
		if err != nil {
			return "", fmt.Errorf("marshal no-args response: %w", err)
		}
		return string(payload), nil
	}

	return strings.Join([]string{
		"ap - agent pipeline runner",
		"",
		"Commands:",
		"  run <spec> <session>   Run a pipeline",
		"  list                   Show available stages",
		"  status [session]       Check session state",
		"  resume <session>       Resume paused/crashed session",
		"  kill <session>         Terminate a session",
		"  logs <session>         Tail session events",
		"  clean [session]        Remove completed sessions",
		"",
		`Spec: ralph | ralph:25 | "a:5 -> b:5" | file.yaml | ./prompt.md`,
		"Flags: -n COUNT  --provider NAME  -m MODEL  -i INPUT  -c CONTEXT  -f  --fg  --json",
		"",
		"Run 'ap <command> --help' for details.",
	}, "\n"), nil
}

// MarshalSuccess returns canonical JSON for success responses with corrections[].
func MarshalSuccess(resp SuccessResponse) ([]byte, error) {
	payload := make(map[string]any, len(resp.Payload)+1)
	for k, v := range resp.Payload {
		payload[k] = v
	}
	payload["corrections"] = resp.Corrections
	return json.Marshal(payload)
}

// MarshalError returns canonical JSON for structured errors.
func MarshalError(resp ErrorResponse) ([]byte, error) {
	errorObj := map[string]any{
		"code":        resp.Error.Code,
		"message":     resp.Error.Message,
		"suggestions": resp.Error.Suggestions,
	}
	if resp.Error.Detail != "" {
		errorObj["detail"] = resp.Error.Detail
	}
	if resp.Error.Syntax != "" {
		errorObj["syntax"] = resp.Error.Syntax
	}
	if len(resp.Error.Available) > 0 {
		keys := make([]string, 0, len(resp.Error.Available))
		for key := range resp.Error.Available {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			errorObj["available_"+key] = resp.Error.Available[key]
		}
	}
	return json.Marshal(map[string]any{"error": errorObj})
}

// NormalizeCode coerces an arbitrary code string to SCREAMING_SNAKE.
func NormalizeCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return "GENERAL_ERROR"
	}
	var b strings.Builder
	lastUnderscore := false
	for i, r := range code {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && i > 0 && !lastUnderscore {
				prev := rune(code[i-1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToUpper(r))
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "GENERAL_ERROR"
	}
	return out
}
