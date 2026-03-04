package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")

	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if !FileExists(path) {
		t.Fatalf("FileExists(%q) = false, want true", path)
	}
}

func TestFileExists_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")

	if FileExists(path) {
		t.Fatalf("FileExists(%q) = true, want false", path)
	}
}
