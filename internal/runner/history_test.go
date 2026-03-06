package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hwells4/ap/internal/store"
)

func TestWriteHistory_EmptyStore(t *testing.T) {
	t.Parallel()

	s := mustOpenStore(t)
	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.md")

	writeHistory(context.Background(), s, "nonexistent-session", historyPath)

	// No file should be written when there are no iterations.
	if _, err := os.Stat(historyPath); err == nil {
		t.Fatal("history.md should not exist for empty store")
	}
}

func TestWriteHistory_SingleStage(t *testing.T) {
	t.Parallel()

	s := mustOpenStore(t)
	ctx := context.Background()

	session := "test-history-single"
	if err := s.CreateSession(ctx, session, "stage", "work", "{}"); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		if err := s.StartIteration(ctx, store.IterationInput{
			SessionName:  session,
			StageName:    "work",
			Iteration:    i,
			ProviderName: "mock",
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteIteration(ctx, store.IterationComplete{
			SessionName:  session,
			StageName:    "work",
			Iteration:    i,
			Decision:     "continue",
			Summary:      "did some work",
			ProviderName: "mock",
		}); err != nil {
			t.Fatal(err)
		}
	}

	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.md")
	writeHistory(ctx, s, session, historyPath)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read history.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Session History") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "## Stage: work") {
		t.Error("missing stage header")
	}
	if strings.Count(content, "**Iteration") != 3 {
		t.Errorf("expected 3 iteration entries, got %d", strings.Count(content, "**Iteration"))
	}
	if !strings.Contains(content, "[continue]") {
		t.Error("missing decision in history entry")
	}
}

func TestWriteHistory_MultiStage(t *testing.T) {
	t.Parallel()

	s := mustOpenStore(t)
	ctx := context.Background()

	session := "test-history-multi"
	if err := s.CreateSession(ctx, session, "pipeline", "demo", "{}"); err != nil {
		t.Fatal(err)
	}

	// Stage 1: plan
	for i := 1; i <= 2; i++ {
		if err := s.StartIteration(ctx, store.IterationInput{
			SessionName: session, StageName: "plan", Iteration: i, ProviderName: "mock",
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteIteration(ctx, store.IterationComplete{
			SessionName: session, StageName: "plan", Iteration: i,
			Decision: "continue", Summary: "planned", ProviderName: "mock",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Stage 2: refine
	if err := s.StartIteration(ctx, store.IterationInput{
		SessionName: session, StageName: "refine", Iteration: 1, ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIteration(ctx, store.IterationComplete{
		SessionName: session, StageName: "refine", Iteration: 1,
		Decision: "stop", Summary: "refined", ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.md")
	writeHistory(ctx, s, session, historyPath)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read history.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Stage: plan") {
		t.Error("missing plan stage header")
	}
	if !strings.Contains(content, "## Stage: refine") {
		t.Error("missing refine stage header")
	}
	if strings.Count(content, "**Iteration") != 3 {
		t.Errorf("expected 3 total iteration entries, got %d", strings.Count(content, "**Iteration"))
	}
}

func TestWriteHistory_TruncatesLongSummaries(t *testing.T) {
	t.Parallel()

	s := mustOpenStore(t)
	ctx := context.Background()

	session := "test-history-truncate"
	if err := s.CreateSession(ctx, session, "stage", "work", "{}"); err != nil {
		t.Fatal(err)
	}

	longSummary := strings.Repeat("x", 300)
	if err := s.StartIteration(ctx, store.IterationInput{
		SessionName: session, StageName: "work", Iteration: 1, ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIteration(ctx, store.IterationComplete{
		SessionName: session, StageName: "work", Iteration: 1,
		Decision: "continue", Summary: longSummary, ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.md")
	writeHistory(ctx, s, session, historyPath)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read history.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "...") {
		t.Error("long summary should be truncated with ...")
	}
	// 200 chars + "..." = 203, plus the prefix, should be well under 300
	if strings.Contains(content, longSummary) {
		t.Error("full 300-char summary should not appear in history")
	}
}

func TestWriteHistory_SkipsInProgressIterations(t *testing.T) {
	t.Parallel()

	s := mustOpenStore(t)
	ctx := context.Background()

	session := "test-history-skip"
	if err := s.CreateSession(ctx, session, "stage", "work", "{}"); err != nil {
		t.Fatal(err)
	}

	// Completed iteration.
	if err := s.StartIteration(ctx, store.IterationInput{
		SessionName: session, StageName: "work", Iteration: 1, ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteIteration(ctx, store.IterationComplete{
		SessionName: session, StageName: "work", Iteration: 1,
		Decision: "continue", Summary: "done", ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}

	// In-progress iteration (started but not completed).
	if err := s.StartIteration(ctx, store.IterationInput{
		SessionName: session, StageName: "work", Iteration: 2, ProviderName: "mock",
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	historyPath := filepath.Join(dir, "history.md")
	writeHistory(ctx, s, session, historyPath)

	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read history.md: %v", err)
	}
	content := string(data)

	if strings.Count(content, "**Iteration") != 1 {
		t.Errorf("expected 1 iteration entry (skipping in-progress), got %d", strings.Count(content, "**Iteration"))
	}
}

func TestAppendStageBoundary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// First call creates the file.
	appendStageBoundary(dir, "plan", 3)

	data, err := os.ReadFile(filepath.Join(dir, "progress.md"))
	if err != nil {
		t.Fatalf("read progress.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "Stage completed: plan (3 iterations)") {
		t.Errorf("missing stage boundary marker, got: %q", content)
	}

	// Second call appends.
	appendStageBoundary(dir, "refine", 2)

	data, err = os.ReadFile(filepath.Join(dir, "progress.md"))
	if err != nil {
		t.Fatalf("read progress.md after append: %v", err)
	}
	content = string(data)

	if !strings.Contains(content, "Stage completed: plan (3 iterations)") {
		t.Error("first marker should still be present")
	}
	if !strings.Contains(content, "Stage completed: refine (2 iterations)") {
		t.Error("second marker should be present")
	}
}
