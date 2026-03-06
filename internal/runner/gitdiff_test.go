package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a temporary git repo with one commit and returns its path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	// Create initial file and commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	return dir
}

func TestIsGitRepo(t *testing.T) {
	repo := initTestRepo(t)
	if !isGitRepo(repo) {
		t.Error("isGitRepo returned false for a valid git repo")
	}

	nonRepo := t.TempDir()
	if isGitRepo(nonRepo) {
		t.Error("isGitRepo returned true for a non-git directory")
	}
}

func TestGitHead(t *testing.T) {
	repo := initTestRepo(t)
	head := gitHead(repo)
	if head == "" {
		t.Error("gitHead returned empty string for a repo with commits")
	}
	if len(head) < 4 || len(head) > 12 {
		t.Errorf("gitHead returned unexpected length: %q", head)
	}

	nonRepo := t.TempDir()
	if got := gitHead(nonRepo); got != "" {
		t.Errorf("gitHead for non-repo = %q, want empty", got)
	}
}

func TestGitDiffStat_AndChangedFiles(t *testing.T) {
	repo := initTestRepo(t)

	preHead := gitHead(repo)

	// Make a change and commit.
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("add", ".")
	run("commit", "-m", "add new file and update readme")

	postHead := gitHead(repo)

	if preHead == postHead {
		t.Fatal("pre and post HEAD should differ after new commit")
	}

	stat := gitDiffStat(repo, preHead, postHead)
	if stat == "" {
		t.Error("gitDiffStat returned empty string after changes")
	}

	files := gitChangedFiles(repo, preHead, postHead)
	if len(files) == 0 {
		t.Fatal("gitChangedFiles returned no files after changes")
	}

	found := map[string]bool{}
	for _, f := range files {
		found[f] = true
	}
	if !found["new.go"] {
		t.Errorf("expected new.go in changed files, got %v", files)
	}
	if !found["README.md"] {
		t.Errorf("expected README.md in changed files, got %v", files)
	}
}

func TestGitDiffStat_NoChanges(t *testing.T) {
	repo := initTestRepo(t)
	head := gitHead(repo)

	stat := gitDiffStat(repo, head, head)
	if stat != "" {
		t.Errorf("gitDiffStat with same pre/post HEAD should be empty, got %q", stat)
	}

	files := gitChangedFiles(repo, head, head)
	if len(files) != 0 {
		t.Errorf("gitChangedFiles with same head should be nil, got %v", files)
	}
}

func TestBuildWorkManifest_NoGitRepo(t *testing.T) {
	dir := t.TempDir()
	m := buildWorkManifest(dir, "")
	if m.Git != nil {
		t.Error("expected nil git section for non-git dir")
	}
}

func TestBuildWorkManifest_NoChanges(t *testing.T) {
	repo := initTestRepo(t)
	head := gitHead(repo)
	m := buildWorkManifest(repo, head)
	if m.Git != nil {
		t.Error("expected nil git section when no changes happened")
	}
}

func TestBuildWorkManifest_WithChanges(t *testing.T) {
	repo := initTestRepo(t)
	preHead := gitHead(repo)

	if err := os.WriteFile(filepath.Join(repo, "feature.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("add", ".")
	run("commit", "-m", "add feature")

	m := buildWorkManifest(repo, preHead)
	if m.Git == nil {
		t.Fatal("expected git section in manifest")
	}
	if m.Git.PreHead != preHead {
		t.Errorf("PreHead = %q, want %q", m.Git.PreHead, preHead)
	}
	if m.Git.PostHead == "" {
		t.Error("PostHead is empty")
	}
	if m.Git.PostHead == preHead {
		t.Error("PostHead should differ from PreHead")
	}
	if m.Git.DiffStat == "" {
		t.Error("DiffStat is empty")
	}
	if len(m.Git.FilesChanged) == 0 {
		t.Error("FilesChanged is empty")
	}
	found := false
	for _, f := range m.Git.FilesChanged {
		if f == "feature.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected feature.go in FilesChanged, got %v", m.Git.FilesChanged)
	}
}
