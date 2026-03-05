package signals

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseValidSignals(t *testing.T) {
	raw := json.RawMessage(`{
    "inject":"critical context",
    "spawn":[{"run":"test-scanner","session":"auth-tests","context":"scan auth","n":3}],
    "escalate":{"type":"human","reason":"need choice","options":["A","B"]}
  }`)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if parsed.Inject != "critical context" {
		t.Fatalf("inject = %q", parsed.Inject)
	}
	if len(parsed.Spawn) != 1 {
		t.Fatalf("spawn count = %d, want 1", len(parsed.Spawn))
	}
	if parsed.Spawn[0].Run != "test-scanner" || parsed.Spawn[0].Session != "auth-tests" {
		t.Fatalf("spawn entry mismatch: %#v", parsed.Spawn[0])
	}
	if parsed.Escalate == nil || parsed.Escalate.Type != "human" {
		t.Fatalf("escalate mismatch: %#v", parsed.Escalate)
	}
	if len(parsed.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", parsed.Warnings)
	}
}

func TestParseReservedSignalsBecomeWarnings(t *testing.T) {
	raw := json.RawMessage(`{
    "checkpoint":{"name":"cp1"},
    "budget":{"remaining":10}
  }`)

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Warnings) != 2 {
		t.Fatalf("warnings = %#v", parsed.Warnings)
	}
	if !strings.Contains(parsed.Warnings[0], "reserved") || !strings.Contains(parsed.Warnings[1], "reserved") {
		t.Fatalf("unexpected warning text: %#v", parsed.Warnings)
	}
}

func TestParseInvalidSpawnShape(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"spawn":"bad"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals.spawn: must be an array" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseInvalidSpawnEntry(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"spawn":[{"run":"x"}]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals.spawn[0].session: is required" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseInvalidEscalate(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"escalate":{"type":"human"}}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals.escalate.reason: is required" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseUnknownSignal(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"mystery":true}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals.mystery: unknown signal" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseInvalidTopLevelType(t *testing.T) {
	_, err := Parse(json.RawMessage(`"bad"`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals: must be a JSON object" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseInvalidSpawnUnknownField(t *testing.T) {
	_, err := Parse(json.RawMessage(`{"spawn":[{"run":"x","session":"y","extra":1}]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "signals: agent_signals.spawn[0].extra: unknown field" {
		t.Fatalf("error = %q", got)
	}
}

func TestParseSpawnProjectRoot(t *testing.T) {
	parsed, err := Parse(json.RawMessage(`{"spawn":[{"run":"ralph","session":"child","project_root":"../other"}]}`))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(parsed.Spawn) != 1 {
		t.Fatalf("spawn count = %d, want 1", len(parsed.Spawn))
	}
	if parsed.Spawn[0].ProjectRoot != "../other" {
		t.Fatalf("project_root = %q, want %q", parsed.Spawn[0].ProjectRoot, "../other")
	}
}
