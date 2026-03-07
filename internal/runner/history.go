package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/store"
)

// writeHistory generates history.md from completed iterations in the store.
// Called before each iteration so the agent sees what prior iterations accomplished.
// Best-effort: errors are silently ignored (history is supplemental, not critical).
func writeHistory(ctx context.Context, s *store.Store, session, path string) {
	rows, err := s.GetIterations(ctx, session, "")
	if err != nil || len(rows) == 0 {
		return
	}

	var buf strings.Builder
	buf.WriteString("# Session History\n\n")

	currentStage := ""
	for _, r := range rows {
		if r.Status != "completed" && r.Status != "failed" {
			continue
		}
		if r.StageName != currentStage {
			if currentStage != "" {
				buf.WriteString("\n")
			}
			buf.WriteString(fmt.Sprintf("## Stage: %s\n\n", r.StageName))
			currentStage = r.StageName
		}
		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			summary = "(no summary)"
		}
		// Truncate long summaries to keep history compact.
		if runes := []rune(summary); len(runes) > 200 {
			summary = string(runes[:200]) + "..."
		}
		buf.WriteString(fmt.Sprintf("- **Iteration %d** [%s]: %s\n", r.Iteration, r.Decision, summary))
	}

	_ = os.WriteFile(path, []byte(buf.String()), 0o644)
}

// appendStageBoundary writes a stage transition marker to the session-scoped progress file.
func appendStageBoundary(runDir, stageName string, iterationsCompleted int) {
	progressPath := filepath.Join(runDir, "progress.md")
	marker := fmt.Sprintf("\n---\n## Stage completed: %s (%d iterations)\n---\n\n",
		stageName, iterationsCompleted)
	f, err := os.OpenFile(progressPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(marker)
}
