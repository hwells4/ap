package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenClose(t *testing.T) {
	s := mustOpen(t)
	if s.DB() == nil {
		t.Fatal("DB() returned nil")
	}
}

func TestCreateGetSession(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	err := s.CreateSession(ctx, "sess1", "stage", "ralph", `{"spec":"ralph:5"}`)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(ctx, "sess1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Name != "sess1" {
		t.Errorf("Name = %q, want %q", got.Name, "sess1")
	}
	if got.Type != "stage" {
		t.Errorf("Type = %q, want %q", got.Type, "stage")
	}
	if got.Pipeline != "ralph" {
		t.Errorf("Pipeline = %q, want %q", got.Pipeline, "ralph")
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
	if got.RunRequestJSON != `{"spec":"ralph:5"}` {
		t.Errorf("RunRequestJSON = %q, want %q", got.RunRequestJSON, `{"spec":"ralph:5"}`)
	}
	if got.StagesJSON != "[]" {
		t.Errorf("StagesJSON = %q, want %q", got.StagesJSON, "[]")
	}
	if got.StartedAt == "" {
		t.Error("StartedAt is empty")
	}
}

func TestGetSessionNotFound(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	_, err := s.GetSession(ctx, "nope")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListSessionsWithFilter(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "a", "stage", "p1", "{}")
	s.CreateSession(ctx, "b", "stage", "p2", "{}")
	s.UpdateSession(ctx, "b", map[string]any{"status": "completed"})

	all, err := s.ListSessions(ctx, "")
	if err != nil {
		t.Fatalf("ListSessions all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListSessions all: got %d, want 2", len(all))
	}

	running, err := s.ListSessions(ctx, "running")
	if err != nil {
		t.Fatalf("ListSessions running: %v", err)
	}
	if len(running) != 1 {
		t.Fatalf("ListSessions running: got %d, want 1", len(running))
	}
	if running[0].Name != "a" {
		t.Errorf("running[0].Name = %q, want %q", running[0].Name, "a")
	}
}

func TestUpdateSession(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")

	err := s.UpdateSession(ctx, "sess1", map[string]any{
		"status":    "paused",
		"iteration": 3,
		"node_id":   "node-abc",
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, _ := s.GetSession(ctx, "sess1")
	if got.Status != "paused" {
		t.Errorf("Status = %q, want %q", got.Status, "paused")
	}
	if got.Iteration != 3 {
		t.Errorf("Iteration = %d, want 3", got.Iteration)
	}
	if got.NodeID != "node-abc" {
		t.Errorf("NodeID = %q, want %q", got.NodeID, "node-abc")
	}
}

func TestUpdateSessionInvalidKey(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")

	err := s.UpdateSession(ctx, "sess1", map[string]any{"bad_key": "x"})
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestUpdateSessionNotFound(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	err := s.UpdateSession(ctx, "nope", map[string]any{"status": "done"})
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateSessionStateMachine(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		// Valid transitions from running.
		{"running->paused", "running", "paused", false},
		{"running->completed", "running", "completed", false},
		{"running->failed", "running", "failed", false},
		{"running->aborted", "running", "aborted", false},
		// Valid transitions from paused.
		{"paused->running", "paused", "running", false},
		{"paused->aborted", "paused", "aborted", false},
		// Valid transitions from failed.
		{"failed->running", "failed", "running", false},
		{"failed->aborted", "failed", "aborted", false},
		// Same-status no-op transitions.
		{"running->running", "running", "running", false},
		{"paused->paused", "paused", "paused", false},
		// Invalid transitions from terminal states.
		{"completed->running", "completed", "running", true},
		{"completed->paused", "completed", "paused", true},
		{"aborted->running", "aborted", "running", true},
		{"aborted->paused", "aborted", "paused", true},
		// Invalid cross-transitions.
		{"paused->completed", "paused", "completed", true},
		{"paused->failed", "paused", "failed", true},
		{"failed->completed", "failed", "completed", true},
		{"failed->paused", "failed", "paused", true},
		{"running->bogus", "running", "bogus", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mustOpen(t)
			ctx := context.Background()

			session := "s-" + tt.name
			s.CreateSession(ctx, session, "stage", "p", "{}")

			// Transition to the "from" state if it's not the default "running".
			if tt.from != "running" {
				// Use a valid path to reach the desired state.
				switch tt.from {
				case "paused":
					s.UpdateSession(ctx, session, map[string]any{"status": "paused"})
				case "completed":
					s.UpdateSession(ctx, session, map[string]any{"status": "completed"})
				case "failed":
					s.UpdateSession(ctx, session, map[string]any{"status": "failed"})
				case "aborted":
					s.UpdateSession(ctx, session, map[string]any{"status": "aborted"})
				}
			}

			err := s.UpdateSession(ctx, session, map[string]any{"status": tt.to})
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %s -> %s, got nil", tt.from, tt.to)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for %s -> %s: %v", tt.from, tt.to, err)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected ErrInvalidTransition, got %v", err)
			}
		})
	}
}

func TestDeleteSessionCascades(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	s.StartIteration(ctx, IterationInput{SessionName: "sess1", StageName: "s1", Iteration: 1})
	s.AppendEvent(ctx, "sess1", "custom", "{}", "{}")

	err := s.DeleteSession(ctx, "sess1")
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Session gone
	_, err = s.GetSession(ctx, "sess1")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Iterations gone (cascaded)
	iters, err := s.GetIterations(ctx, "sess1", "")
	if err != nil {
		t.Fatalf("GetIterations after delete: %v", err)
	}
	if len(iters) != 0 {
		t.Errorf("expected 0 iterations after cascade, got %d", len(iters))
	}

	// Events gone (cascaded)
	events, err := s.GetEvents(ctx, "sess1", "", 0)
	if err != nil {
		t.Fatalf("GetEvents after delete: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events after cascade, got %d", len(events))
	}
}

func TestStartCompleteIteration(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")

	err := s.StartIteration(ctx, IterationInput{
		SessionName: "sess1",
		StageName:   "ralph",
		Iteration:   1,
	})
	if err != nil {
		t.Fatalf("StartIteration: %v", err)
	}

	// Check session updated
	sess, _ := s.GetSession(ctx, "sess1")
	if sess.Iteration != 1 {
		t.Errorf("session.Iteration = %d, want 1", sess.Iteration)
	}

	err = s.CompleteIteration(ctx, IterationComplete{
		SessionName: "sess1",
		StageName:   "ralph",
		Iteration:   1,
		Decision:    "continue",
		Summary:     "all good",
		ExitCode:    0,
		SignalsJSON: "{}",
		Stdout:      "hello",
		Stderr:      "",
		ContextJSON: `{"key":"val"}`,
	})
	if err != nil {
		t.Fatalf("CompleteIteration: %v", err)
	}

	// Check session updated
	sess, _ = s.GetSession(ctx, "sess1")
	if sess.IterationCompleted != 1 {
		t.Errorf("session.IterationCompleted = %d, want 1", sess.IterationCompleted)
	}

	iters, err := s.GetIterations(ctx, "sess1", "")
	if err != nil {
		t.Fatalf("GetIterations: %v", err)
	}
	if len(iters) != 1 {
		t.Fatalf("got %d iterations, want 1", len(iters))
	}
	if iters[0].Status != "completed" {
		t.Errorf("iteration status = %q, want completed", iters[0].Status)
	}
	if iters[0].Decision != "continue" {
		t.Errorf("iteration decision = %q, want continue", iters[0].Decision)
	}
}

func TestGetIterationsWithStageFilter(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	s.StartIteration(ctx, IterationInput{SessionName: "sess1", StageName: "s1", Iteration: 1})
	s.StartIteration(ctx, IterationInput{SessionName: "sess1", StageName: "s2", Iteration: 2})

	filtered, err := s.GetIterations(ctx, "sess1", "s1")
	if err != nil {
		t.Fatalf("GetIterations filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("got %d, want 1", len(filtered))
	}
	if filtered[0].StageName != "s1" {
		t.Errorf("stage = %q, want s1", filtered[0].StageName)
	}
}

func TestAppendEventMonotonicSeq(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")

	for i := 0; i < 3; i++ {
		if err := s.AppendEvent(ctx, "sess1", "test", "{}", "{}"); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	events, err := s.GetEvents(ctx, "sess1", "", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Errorf("event[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}

func TestGetEventsTypeFilter(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	s.AppendEvent(ctx, "sess1", "alpha", "{}", "{}")
	s.AppendEvent(ctx, "sess1", "beta", "{}", "{}")
	s.AppendEvent(ctx, "sess1", "alpha", "{}", "{}")

	events, err := s.GetEvents(ctx, "sess1", "alpha", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestTailEvents(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	s.AppendEvent(ctx, "sess1", "a", "{}", "{}")
	s.AppendEvent(ctx, "sess1", "b", "{}", "{}")
	s.AppendEvent(ctx, "sess1", "c", "{}", "{}")

	tail, err := s.TailEvents(ctx, "sess1", 2)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(tail) != 1 {
		t.Fatalf("got %d, want 1", len(tail))
	}
	if tail[0].Seq != 3 {
		t.Errorf("Seq = %d, want 3", tail[0].Seq)
	}
}

func TestAddGetChildren(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.AddChild(ctx, "parent", "child-b")
	s.AddChild(ctx, "parent", "child-a")
	s.AddChild(ctx, "parent", "child-a") // duplicate, should be ignored

	children, err := s.GetChildren(ctx, "parent")
	if err != nil {
		t.Fatalf("GetChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2", len(children))
	}
	if children[0] != "child-a" || children[1] != "child-b" {
		t.Errorf("children = %v, want [child-a, child-b]", children)
	}
}

func TestCreateCompleteGetStages(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "chain", "p", "{}")

	stages := []StageInput{
		{Name: "plan", Index: 0, Iterations: 5},
		{Name: "code", Index: 1, Iterations: 10},
	}
	err := s.CreateStages(ctx, "sess1", stages)
	if err != nil {
		t.Fatalf("CreateStages: %v", err)
	}

	got, err := s.GetStages(ctx, "sess1")
	if err != nil {
		t.Fatalf("GetStages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d stages, want 2", len(got))
	}
	if got[0].Name != "plan" || got[1].Name != "code" {
		t.Errorf("stages = %+v", got)
	}

	err = s.CompleteStage(ctx, "sess1", 0, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("CompleteStage: %v", err)
	}

	got, _ = s.GetStages(ctx, "sess1")
	if got[0].CompletedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("CompletedAt = %q, want 2026-01-01T00:00:00Z", got[0].CompletedAt)
	}
	if got[1].CompletedAt != "" {
		t.Errorf("stage[1].CompletedAt should be empty, got %q", got[1].CompletedAt)
	}
}

func TestCompleteStageNotFound(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	s.CreateStages(ctx, "sess1", []StageInput{{Name: "a", Index: 0, Iterations: 1}})

	err := s.CompleteStage(ctx, "sess1", 99, "now")
	if err == nil {
		t.Fatal("expected error for nonexistent stage index")
	}
}

func TestConcurrentAppendEvent(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	s.CreateSession(ctx, "sess1", "stage", "p", "{}")

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.AppendEvent(ctx, "sess1", "concurrent", "{}", "{}"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent AppendEvent error: %v", err)
	}

	events, err := s.GetEvents(ctx, "sess1", "", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != n {
		t.Fatalf("got %d events, want %d", len(events), n)
	}

	// Check monotonic and unique seqs
	seen := make(map[int]bool)
	for _, e := range events {
		if seen[e.Seq] {
			t.Errorf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[i] {
			t.Errorf("missing seq %d", i)
		}
	}
}

func TestSchemaMigrationIdempotent(t *testing.T) {
	s := mustOpen(t)
	// Run migrate again — should be a no-op
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	// The store should still work
	ctx := context.Background()
	err := s.CreateSession(ctx, "sess1", "stage", "p", "{}")
	if err != nil {
		t.Fatalf("CreateSession after double migrate: %v", err)
	}
}
