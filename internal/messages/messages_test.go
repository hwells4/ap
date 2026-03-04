package messages

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriter_Append(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")

	w := NewWriter(path)
	if err := w.Append(Message{
		Timestamp: "2026-03-04T00:00:00Z",
		From:      "claude",
		Type:      "progress",
		Content:   "step 1 done",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].From != "claude" {
		t.Fatalf("from = %q, want claude", msgs[0].From)
	}
	if msgs[0].Content != "step 1 done" {
		t.Fatalf("content = %q, want 'step 1 done'", msgs[0].Content)
	}
}

func TestWriter_Append_MultipleMessages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")

	w := NewWriter(path)
	for i := 0; i < 5; i++ {
		if err := w.Append(Message{
			Timestamp: "2026-03-04T00:00:00Z",
			From:      "codex",
			Type:      "status",
			Content:   "iteration",
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("got %d messages, want 5", len(msgs))
	}
}

func TestWriter_Append_WithRef(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")

	w := NewWriter(path)
	if err := w.Append(Message{
		Timestamp: "2026-03-04T00:00:00Z",
		From:      "gemini",
		Type:      "reference",
		Content:   "see attached",
		Ref:       "file:///tmp/output.md",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if msgs[0].Ref != "file:///tmp/output.md" {
		t.Fatalf("ref = %q, want file:///tmp/output.md", msgs[0].Ref)
	}
}

func TestWriter_Append_AutoTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")

	w := NewWriter(path)
	if err := w.Append(Message{
		From:    "claude",
		Type:    "info",
		Content: "auto ts",
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if msgs[0].Timestamp == "" {
		t.Fatal("timestamp should be auto-filled")
	}
}

func TestReadAll_FileNotExist(t *testing.T) {
	t.Parallel()
	msgs, err := ReadAll("/nonexistent/messages.jsonl")
	if err != nil {
		t.Fatalf("ReadAll should not error on missing file: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want 0", len(msgs))
	}
}

func TestReadAll_EmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want 0", len(msgs))
	}
}

func TestWriter_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")

	w := NewWriter(path)
	var wg sync.WaitGroup
	count := 20

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Append(Message{
				From:    "worker",
				Type:    "progress",
				Content: "done",
			})
		}()
	}
	wg.Wait()

	msgs, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(msgs) != count {
		t.Fatalf("got %d messages, want %d", len(msgs), count)
	}
}

func TestPathForSession(t *testing.T) {
	t.Parallel()
	got := PathForSession("/home/user/.ap/runs/my-session")
	want := "/home/user/.ap/runs/my-session/messages.jsonl"
	if got != want {
		t.Fatalf("PathForSession = %q, want %q", got, want)
	}
}

func TestWriter_Path(t *testing.T) {
	t.Parallel()
	w := NewWriter("/tmp/test/messages.jsonl")
	if w.Path() != "/tmp/test/messages.jsonl" {
		t.Fatalf("Path() = %q, want /tmp/test/messages.jsonl", w.Path())
	}
}
