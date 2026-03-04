// Package signals parses and validates agent_signals payloads.
package signals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// AgentSignals is the typed representation of agent_signals from status.json.
type AgentSignals struct {
	Inject   string          `json:"inject,omitempty"`
	Spawn    []SpawnSignal   `json:"spawn"`
	Escalate *EscalateSignal `json:"escalate,omitempty"`
	Warnings []string        `json:"warnings"`
}

// SpawnSignal requests launching a child session.
type SpawnSignal struct {
	Run     string `json:"run"`
	Session string `json:"session"`
	Context string `json:"context,omitempty"`
	N       int    `json:"n,omitempty"`
}

// EscalateSignal requests escalation to a human/external decision path.
type EscalateSignal struct {
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Options []string `json:"options"`
}

// ValidationError is a deterministic parse/validation error for agent_signals.
type ValidationError struct {
	Path    string
	Message string
}

func (e ValidationError) Error() string {
	path := strings.TrimSpace(e.Path)
	msg := strings.TrimSpace(e.Message)
	if path == "" {
		return fmt.Sprintf("signals: %s", msg)
	}
	return fmt.Sprintf("signals: %s: %s", path, msg)
}

// Parse validates and decodes agent_signals from raw JSON.
func Parse(raw json.RawMessage) (AgentSignals, error) {
	out := AgentSignals{
		Spawn:    []SpawnSignal{},
		Warnings: []string{},
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return out, nil
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return AgentSignals{}, ValidationError{
			Path:    "agent_signals",
			Message: "must be a JSON object",
		}
	}

	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		rawValue := root[key]
		path := "agent_signals." + key

		switch key {
		case "inject":
			value, ok, err := parseOptionalString(rawValue, path)
			if err != nil {
				return AgentSignals{}, err
			}
			if ok {
				out.Inject = value
			}
		case "spawn":
			spawn, err := parseSpawn(rawValue, path)
			if err != nil {
				return AgentSignals{}, err
			}
			out.Spawn = spawn
		case "escalate":
			escalate, ok, err := parseEscalate(rawValue, path)
			if err != nil {
				return AgentSignals{}, err
			}
			if ok {
				out.Escalate = escalate
			}
		case "checkpoint", "budget":
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s is reserved and was ignored", path))
		default:
			return AgentSignals{}, ValidationError{
				Path:    path,
				Message: "unknown signal",
			}
		}
	}

	return out, nil
}

func parseSpawn(raw json.RawMessage, path string) ([]SpawnSignal, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return []SpawnSignal{}, nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, ValidationError{
			Path:    path,
			Message: "must be an array",
		}
	}

	out := make([]SpawnSignal, 0, len(entries))
	for idx, entry := range entries {
		signal, err := parseSpawnEntry(entry, fmt.Sprintf("%s[%d]", path, idx))
		if err != nil {
			return nil, err
		}
		out = append(out, signal)
	}
	return out, nil
}

func parseSpawnEntry(raw json.RawMessage, path string) (SpawnSignal, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return SpawnSignal{}, ValidationError{
			Path:    path,
			Message: "must be an object",
		}
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch key {
		case "run", "session", "context", "n":
		default:
			return SpawnSignal{}, ValidationError{
				Path:    path + "." + key,
				Message: "unknown field",
			}
		}
	}

	run, err := parseRequiredString(obj, path+".run")
	if err != nil {
		return SpawnSignal{}, err
	}
	session, err := parseRequiredString(obj, path+".session")
	if err != nil {
		return SpawnSignal{}, err
	}
	contextValue, _, err := parseOptionalString(obj["context"], path+".context")
	if err != nil {
		return SpawnSignal{}, err
	}

	count := 0
	if rawCount, ok := obj["n"]; ok && !bytes.Equal(bytes.TrimSpace(rawCount), []byte("null")) {
		var asFloat float64
		if err := json.Unmarshal(rawCount, &asFloat); err != nil {
			return SpawnSignal{}, ValidationError{
				Path:    path + ".n",
				Message: "must be a positive integer",
			}
		}
		if math.Trunc(asFloat) != asFloat || asFloat <= 0 {
			return SpawnSignal{}, ValidationError{
				Path:    path + ".n",
				Message: "must be a positive integer",
			}
		}
		count = int(asFloat)
	}

	return SpawnSignal{
		Run:     run,
		Session: session,
		Context: contextValue,
		N:       count,
	}, nil
}

func parseEscalate(raw json.RawMessage, path string) (*EscalateSignal, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, false, ValidationError{
			Path:    path,
			Message: "must be an object",
		}
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch key {
		case "type", "reason", "options":
		default:
			return nil, false, ValidationError{
				Path:    path + "." + key,
				Message: "unknown field",
			}
		}
	}

	escalateType, err := parseRequiredString(obj, path+".type")
	if err != nil {
		return nil, false, err
	}
	reason, err := parseRequiredString(obj, path+".reason")
	if err != nil {
		return nil, false, err
	}

	options := []string{}
	if rawOptions, ok := obj["options"]; ok {
		trimmedOptions := bytes.TrimSpace(rawOptions)
		if !bytes.Equal(trimmedOptions, []byte("null")) {
			var parsed []string
			if err := json.Unmarshal(rawOptions, &parsed); err != nil {
				return nil, false, ValidationError{
					Path:    path + ".options",
					Message: "must be an array of strings",
				}
			}
			options = parsed
		}
	}

	return &EscalateSignal{
		Type:    escalateType,
		Reason:  reason,
		Options: options,
	}, true, nil
}

func parseOptionalString(raw json.RawMessage, path string) (value string, present bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}

	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", false, ValidationError{
			Path:    path,
			Message: "must be a string",
		}
	}
	return strings.TrimSpace(value), true, nil
}

func parseRequiredString(fields map[string]json.RawMessage, path string) (string, error) {
	parts := strings.Split(path, ".")
	field := parts[len(parts)-1]
	raw, ok := fields[field]
	if !ok {
		return "", ValidationError{
			Path:    path,
			Message: "is required",
		}
	}

	value, present, err := parseOptionalString(raw, path)
	if err != nil {
		return "", err
	}
	if !present || value == "" {
		return "", ValidationError{
			Path:    path,
			Message: "must be a non-empty string",
		}
	}
	return value, nil
}
