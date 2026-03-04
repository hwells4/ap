package fuzzy

import "testing"

func TestNormalizeCommandAlias(t *testing.T) {
	command, correction, ok := NormalizeCommand("start")
	if !ok {
		t.Fatal("expected command to normalize")
	}
	if command != "run" {
		t.Fatalf("command = %q, want run", command)
	}
	if correction == nil || correction.From != "start" || correction.To != "run" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
}

func TestNormalizeCommandTypo(t *testing.T) {
	command, correction, ok := NormalizeCommand("listt")
	if !ok {
		t.Fatal("expected typo correction for list")
	}
	if command != "list" {
		t.Fatalf("command = %q, want list", command)
	}
	if correction == nil || correction.To != "list" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
}

func TestNormalizeCommandDestructiveSafety(t *testing.T) {
	command, correction, ok := NormalizeCommand("kil")
	if ok {
		t.Fatalf("expected destructive typo to be rejected, got command=%q correction=%#v", command, correction)
	}
}

func TestNormalizeCommandDestructiveAliasAllowed(t *testing.T) {
	command, correction, ok := NormalizeCommand("stop")
	if !ok {
		t.Fatal("expected stop alias to normalize")
	}
	if command != "kill" {
		t.Fatalf("command = %q, want kill", command)
	}
	if correction == nil || correction.To != "kill" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
}

func TestNormalizeProviderAlias(t *testing.T) {
	provider, correction := NormalizeProvider("anthropic")
	if provider != "claude" {
		t.Fatalf("provider = %q, want claude", provider)
	}
	if correction == nil || correction.From != "anthropic" || correction.To != "claude" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
}

func TestNormalizeStage(t *testing.T) {
	stage, correction, ok := NormalizeStage("ralhp", []string{"ralph", "review"})
	if !ok {
		t.Fatal("expected stage typo correction")
	}
	if stage != "ralph" {
		t.Fatalf("stage = %q, want ralph", stage)
	}
	if correction == nil || correction.To != "ralph" {
		t.Fatalf("unexpected correction: %#v", correction)
	}
}

func TestSuggestStages(t *testing.T) {
	suggestions := SuggestStages("ralhp", []string{"ralph", "review", "elegance"}, 2)
	if len(suggestions) == 0 {
		t.Fatal("expected stage suggestions")
	}
	if suggestions[0] != "ralph" {
		t.Fatalf("first suggestion = %q, want ralph", suggestions[0])
	}
}
