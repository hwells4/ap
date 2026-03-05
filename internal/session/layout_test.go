package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCreatesDeterministicSessionTree(t *testing.T) {
	root := t.TempDir()

	layout, err := Prepare(root, "my-session", PrepareOptions{
		StageName:  "ralph",
		StageIndex: 1,
		Iteration:  2,
	})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}

	for _, dir := range []string{
		layout.RunsRoot,
		layout.SessionDir,
		layout.StageDir,
		layout.IterationDir,
	} {
		assertDir(t, dir)
	}

	sessionPrefix := layout.SessionDir + string(os.PathSeparator)
	for _, path := range []string{
		layout.RunRequestPath,
		layout.StatePath,
		layout.EventsPath,
		layout.ProgressPath,
		layout.OutputPath,
	} {
		if !strings.HasPrefix(path, sessionPrefix) {
			t.Fatalf("path escaped session root: %s (session: %s)", path, layout.SessionDir)
		}
	}
}

func TestPrepareIsIdempotentWithoutForce(t *testing.T) {
	root := t.TempDir()

	layout, err := Prepare(root, "idempotent", PrepareOptions{
		StageName: "ralph",
	})
	if err != nil {
		t.Fatalf("first prepare failed: %v", err)
	}

	sentinel := filepath.Join(layout.SessionDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	layout2, err := Prepare(root, "idempotent", PrepareOptions{
		StageName: "ralph",
	})
	if err != nil {
		t.Fatalf("second prepare failed: %v", err)
	}

	if layout.SessionDir != layout2.SessionDir {
		t.Fatalf("session dir changed across idempotent runs: %s vs %s", layout.SessionDir, layout2.SessionDir)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected sentinel to survive idempotent run: %v", err)
	}
}

func TestPrepareForceRecreatesSessionTree(t *testing.T) {
	root := t.TempDir()

	layout, err := Prepare(root, "force-session", PrepareOptions{
		StageName: "ralph",
	})
	if err != nil {
		t.Fatalf("first prepare failed: %v", err)
	}

	sentinel := filepath.Join(layout.SessionDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("delete me"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	layout2, err := Prepare(root, "force-session", PrepareOptions{
		StageName: "ralph",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("force prepare failed: %v", err)
	}

	assertDir(t, layout2.SessionDir)
	assertDir(t, layout2.StageDir)
	assertDir(t, layout2.IterationDir)

	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected force prepare to recreate tree and remove sentinel, err=%v", err)
	}
}

func assertDir(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got file: %s", path)
	}
}
