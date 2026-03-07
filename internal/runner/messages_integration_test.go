package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hwells4/ap/internal/messages"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

// TestMessages_PathInContext verifies that the messages.jsonl path is included
// in the context.json generated for each iteration.
func TestMessages_PathInContext(t *testing.T) {
	runDir, s := tempSession(t)

	mp := mock.New(
		mock.WithResponses(
			mock.ContinueResponse("single iteration"),
		),
	)

	cfg := Config{
		Session:        "msg-path-test",
		RunDir:         runDir,
		StageName:      "test-stage",
		Provider:       mp,
		Iterations:     1,
		PromptTemplate: "iteration ${ITERATION}",
		Store:          s,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	// Read context.json from the iteration directory.
	ctxPath := filepath.Join(runDir, "stage-00-test-stage", "iterations", "001", "context.json")
	ctxData, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read context.json: %v", err)
	}

	var ctx struct {
		Paths struct {
			Messages string `json:"messages"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(ctxData, &ctx); err != nil {
		t.Fatalf("parse context.json: %v", err)
	}

	expectedMsgPath := filepath.Join(runDir, "messages.jsonl")
	if ctx.Paths.Messages != expectedMsgPath {
		t.Errorf("paths.messages = %q, want %q", ctx.Paths.Messages, expectedMsgPath)
	}
}

// TestMessages_WriteAndReadRoundTrip verifies that messages written via the
// Writer can be read back via ReadAll at the session's canonical path.
func TestMessages_WriteAndReadRoundTrip(t *testing.T) {
	runDir := t.TempDir()
	msgPath := messages.PathForSession(runDir)

	// Verify the canonical path.
	expectedPath := filepath.Join(runDir, "messages.jsonl")
	if msgPath != expectedPath {
		t.Fatalf("PathForSession = %q, want %q", msgPath, expectedPath)
	}

	// Write messages.
	w := messages.NewWriter(msgPath)
	if w.Path() != msgPath {
		t.Fatalf("writer path = %q, want %q", w.Path(), msgPath)
	}

	msg1 := messages.Message{
		From:    "agent-1",
		Type:    "status",
		Content: "starting work",
	}
	msg2 := messages.Message{
		From:    "orchestrator",
		Type:    "instruction",
		Content: "proceed to phase 2",
		Ref:     "task-42",
	}

	if err := w.Append(msg1); err != nil {
		t.Fatalf("append msg1: %v", err)
	}
	if err := w.Append(msg2); err != nil {
		t.Fatalf("append msg2: %v", err)
	}

	// Read back.
	msgs, err := messages.ReadAll(msgPath)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}

	if msgs[0].From != "agent-1" || msgs[0].Type != "status" || msgs[0].Content != "starting work" {
		t.Errorf("msg[0] = %+v, want agent-1/status/starting work", msgs[0])
	}
	if msgs[1].From != "orchestrator" || msgs[1].Ref != "task-42" {
		t.Errorf("msg[1] = %+v, want orchestrator/ref=task-42", msgs[1])
	}

	// Timestamps should be auto-populated.
	if msgs[0].Timestamp == "" {
		t.Error("msg[0] timestamp should be auto-populated")
	}
	if msgs[1].Timestamp == "" {
		t.Error("msg[1] timestamp should be auto-populated")
	}
}
