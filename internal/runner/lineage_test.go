package runner

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

func TestRun_ParentSession_RecordedInState(t *testing.T) {
	runDir := tempSession(t)
	mp := mock.New(mock.WithResponses(mock.ContinueResponse("done")))
	_, err := Run(context.Background(), Config{
		Session: "child-session", RunDir: runDir, StageName: "test-stage",
		Provider: mp, Iterations: 1, PromptTemplate: "iteration ${ITERATION}",
		ParentSession: "parent-session",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.ParentSession != "parent-session" {
		t.Errorf("ParentSession = %q, want %q", st.ParentSession, "parent-session")
	}
}

func TestRun_ParentSession_EmptyWhenRoot(t *testing.T) {
	runDir := tempSession(t)
	mp := mock.New(mock.WithResponses(mock.ContinueResponse("done")))
	_, err := Run(context.Background(), Config{
		Session: "root-session", RunDir: runDir, StageName: "test-stage",
		Provider: mp, Iterations: 1, PromptTemplate: "iteration ${ITERATION}",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.ParentSession != "" {
		t.Errorf("ParentSession = %q, want empty", st.ParentSession)
	}
}

func TestRun_SessionStartEvent_IncludesParent(t *testing.T) {
	runDir := tempSession(t)
	mp := mock.New(mock.WithResponses(mock.ContinueResponse("done")))
	_, err := Run(context.Background(), Config{
		Session: "child-events", RunDir: runDir, StageName: "test-stage",
		Provider: mp, Iterations: 1, PromptTemplate: "iteration ${ITERATION}",
		ParentSession: "my-parent",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	evts := readEvents(t, runDir)
	starts := filterByType(evts, events.TypeSessionStart)
	if len(starts) != 1 {
		t.Fatalf("session_start events = %d, want 1", len(starts))
	}
	if starts[0].Data["parent_session"] != "my-parent" {
		t.Errorf("session_start parent_session = %v, want my-parent", starts[0].Data["parent_session"])
	}
}

func TestRun_SessionStartEvent_NoParentWhenRoot(t *testing.T) {
	runDir := tempSession(t)
	mp := mock.New(mock.WithResponses(mock.ContinueResponse("done")))
	_, err := Run(context.Background(), Config{
		Session: "root-events", RunDir: runDir, StageName: "test-stage",
		Provider: mp, Iterations: 1, PromptTemplate: "iteration ${ITERATION}",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	evts := readEvents(t, runDir)
	starts := filterByType(evts, events.TypeSessionStart)
	if len(starts) != 1 {
		t.Fatalf("session_start events = %d, want 1", len(starts))
	}
	if _, exists := starts[0].Data["parent_session"]; exists {
		t.Errorf("root session_start should not have parent_session, got %v", starts[0].Data["parent_session"])
	}
}

func TestRun_ChildSessions_TrackedInState(t *testing.T) {
	st := &state.SessionState{Session: "parent", Status: state.StateRunning}
	st.AddChildSession("child-1")
	st.AddChildSession("child-2")
	st.AddChildSession("child-1") // duplicate
	if len(st.ChildSessions) != 2 {
		t.Errorf("ChildSessions len = %d, want 2", len(st.ChildSessions))
	}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded state.SessionState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(loaded.ChildSessions) != 2 {
		t.Errorf("after round-trip: ChildSessions len = %d, want 2", len(loaded.ChildSessions))
	}
	st.AddChildSession("")
	if len(st.ChildSessions) != 2 {
		t.Errorf("after empty add: ChildSessions len = %d, want 2", len(st.ChildSessions))
	}
	var nilState *state.SessionState
	nilState.AddChildSession("x") // should not panic
}

func TestRun_ParentSession_PersistedAcrossIterations(t *testing.T) {
	runDir := tempSession(t)
	mp := mock.New(mock.WithResponses(
		mock.ContinueResponse("iter 1"),
		mock.ContinueResponse("iter 2"),
	))
	_, err := Run(context.Background(), Config{
		Session: "persist-parent", RunDir: runDir, StageName: "test-stage",
		Provider: mp, Iterations: 2, PromptTemplate: "iteration ${ITERATION}",
		ParentSession: "my-parent",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	st, err := state.Load(runDir + "/state.json")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if st.ParentSession != "my-parent" {
		t.Errorf("ParentSession after 2 iterations = %q, want %q", st.ParentSession, "my-parent")
	}
	data, err := os.ReadFile(runDir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if raw["parent_session"] != "my-parent" {
		t.Errorf("raw parent_session = %v, want my-parent", raw["parent_session"])
	}
}
