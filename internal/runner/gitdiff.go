package runner

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 5 * time.Second

// isGitRepo reports whether workDir is inside a git repository.
// Returns false on any error.
func isGitRepo(workDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = workDir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitHead returns the short HEAD commit hash. Empty on error.
func gitHead(workDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short", "HEAD")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitDiffStat returns `git diff --stat HEAD~1` summary text.
// Uses the preHead..postHead range when both are provided.
// Returns empty on error.
func gitDiffStat(workDir, preHead, postHead string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if preHead != "" && postHead != "" {
		cmd = exec.CommandContext(ctx, "git", "diff", "--stat", preHead+".."+postHead)
	} else {
		cmd = exec.CommandContext(ctx, "git", "diff", "--stat", "HEAD")
	}
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitChangedFiles returns a list of files changed between preHead and postHead.
// Returns nil on error.
func gitChangedFiles(workDir, preHead, postHead string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if preHead != "" && postHead != "" {
		cmd = exec.CommandContext(ctx, "git", "diff", "--name-only", preHead+".."+postHead)
	} else {
		cmd = exec.CommandContext(ctx, "git", "diff", "--name-only", "HEAD")
	}
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
