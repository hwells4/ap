package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

func TestQuerySessionsDefaultsToInstanceScope(t *testing.T) {
	t.Setenv("AP_CONTROL_DB", filepath.Join(t.TempDir(), "control.db"))
	ctx := context.Background()

	projectA := t.TempDir()
	a, err := store.Open(filepath.Join(projectA, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateSession(ctx, "alpha", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateSession(ctx, "alpha", map[string]any{
		"project_root": projectA,
		"status":       "running",
	}); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	projectB := t.TempDir()
	b, err := store.Open(filepath.Join(projectB, ".ap", "ap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.CreateSession(ctx, "beta", "loop", "", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := b.UpdateSession(ctx, "beta", map[string]any{
		"project_root": projectB,
		"status":       "paused",
	}); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()

	var stdout, stderr bytes.Buffer
	deps := cliDeps{
		mode:   output.ModeJSON,
		stdout: &stdout,
		stderr: &stderr,
		getwd:  func() (string, error) { return t.TempDir(), nil },
	}

	code := runQuery([]string{"sessions", "--json"}, deps)
	if code != output.ExitSuccess {
		t.Fatalf("exit code = %d; stderr: %s", code, stderr.String())
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	if got := int(result["count"].(float64)); got != 2 {
		t.Fatalf("count = %d, want 2; payload=%#v", got, result)
	}
}
