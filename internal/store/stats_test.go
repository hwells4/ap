package store

import (
	"context"
	"testing"
)

func TestGetStageStats_Empty(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	stats, err := s.GetStageStats(context.Background())
	if err != nil {
		t.Fatalf("GetStageStats: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("expected empty stats, got %d entries", len(stats))
	}
}

func TestGetStageStats_Aggregation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create two sessions using "codegen" stage and one using "ralph".
	if err := s.CreateSession(ctx, "sess-1", "stage", "codegen", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "sess-2", "stage", "codegen", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "sess-3", "stage", "ralph", "{}"); err != nil {
		t.Fatal(err)
	}

	// Add iterations.
	for i := 1; i <= 3; i++ {
		s.StartIteration(ctx, IterationInput{SessionName: "sess-1", StageName: "codegen", Iteration: i})
		s.CompleteIteration(ctx, IterationComplete{SessionName: "sess-1", StageName: "codegen", Iteration: i, Decision: "continue", Summary: "did stuff"})
	}
	for i := 1; i <= 2; i++ {
		s.StartIteration(ctx, IterationInput{SessionName: "sess-2", StageName: "codegen", Iteration: i})
		s.CompleteIteration(ctx, IterationComplete{SessionName: "sess-2", StageName: "codegen", Iteration: i, Decision: "continue", Summary: "more stuff"})
	}
	s.StartIteration(ctx, IterationInput{SessionName: "sess-3", StageName: "ralph", Iteration: 1})
	s.CompleteIteration(ctx, IterationComplete{SessionName: "sess-3", StageName: "ralph", Iteration: 1, Decision: "stop", Summary: "done"})

	// Complete one session, fail another.
	s.UpdateSession(ctx, "sess-1", map[string]any{"status": "completed"})
	s.UpdateSession(ctx, "sess-2", map[string]any{"status": "failed"})
	s.UpdateSession(ctx, "sess-3", map[string]any{"status": "completed"})

	stats, err := s.GetStageStats(ctx)
	if err != nil {
		t.Fatalf("GetStageStats: %v", err)
	}

	// Check codegen stats.
	cg, ok := stats["codegen"]
	if !ok {
		t.Fatal("expected codegen stats")
	}
	if cg.TotalSessions != 2 {
		t.Fatalf("codegen TotalSessions = %d, want 2", cg.TotalSessions)
	}
	if cg.TotalIterations != 5 {
		t.Fatalf("codegen TotalIterations = %d, want 5", cg.TotalIterations)
	}
	if cg.Completed != 1 {
		t.Fatalf("codegen Completed = %d, want 1", cg.Completed)
	}
	if cg.Failed != 1 {
		t.Fatalf("codegen Failed = %d, want 1", cg.Failed)
	}
	if cg.AvgIterations != 2.5 {
		t.Fatalf("codegen AvgIterations = %f, want 2.5", cg.AvgIterations)
	}

	// Check ralph stats.
	r, ok := stats["ralph"]
	if !ok {
		t.Fatal("expected ralph stats")
	}
	if r.TotalSessions != 1 {
		t.Fatalf("ralph TotalSessions = %d, want 1", r.TotalSessions)
	}
	if r.TotalIterations != 1 {
		t.Fatalf("ralph TotalIterations = %d, want 1", r.TotalIterations)
	}
	if r.Completed != 1 {
		t.Fatalf("ralph Completed = %d, want 1", r.Completed)
	}
}
