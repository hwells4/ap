package runtarget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	target, err := Resolve(root, SourceCLI)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if target.ProjectRoot != root {
		t.Fatalf("ProjectRoot = %q, want %q", target.ProjectRoot, root)
	}
	if target.ConfigRoot != root {
		t.Fatalf("ConfigRoot = %q, want %q", target.ConfigRoot, root)
	}
	if target.ProjectKey != root {
		t.Fatalf("ProjectKey = %q, want %q", target.ProjectKey, root)
	}
	if target.Source != SourceCLI {
		t.Fatalf("Source = %q, want %q", target.Source, SourceCLI)
	}
}

func TestResolveSpawnRoot(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	got, err := ResolveSpawnRoot(parent, "child")
	if err != nil {
		t.Fatalf("ResolveSpawnRoot() error: %v", err)
	}
	if got != child {
		t.Fatalf("ResolveSpawnRoot() = %q, want %q", got, child)
	}
}
