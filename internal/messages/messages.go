// Package messages provides a live message bus for mid-iteration agent
// communication. Messages are stored in an append-only JSONL file with
// file-level locking for concurrent writers.
package messages

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Message represents a single message in the bus.
type Message struct {
	Timestamp string `json:"timestamp"`
	From      string `json:"from"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	Ref       string `json:"ref,omitempty"`
}

// Writer appends messages to a messages.jsonl file with file-level locking.
type Writer struct {
	path string
	mu   sync.Mutex
}

// NewWriter creates a Writer for the given messages.jsonl path. The parent
// directory is created on the first Append call.
func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

// Path returns the underlying file path.
func (w *Writer) Path() string {
	return w.path
}

// Append writes a message to the bus. Safe for concurrent use.
func (w *Writer) Append(msg Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if msg.Timestamp == "" {
		msg.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("messages: marshal: %w", err)
	}
	payload = append(payload, '\n')

	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return fmt.Errorf("messages: mkdir: %w", err)
	}

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("messages: open: %w", err)
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("messages: lock: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN) //nolint:errcheck

	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("messages: seek: %w", err)
	}

	n, err := file.Write(payload)
	if err != nil || n != len(payload) {
		_ = file.Truncate(start)
		if err != nil {
			return fmt.Errorf("messages: write: %w", err)
		}
		return fmt.Errorf("messages: write: short write")
	}
	return nil
}

// ReadAll reads all messages from the bus file. Returns an empty slice if the
// file does not exist.
func ReadAll(path string) ([]Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, fmt.Errorf("messages: read: %w", err)
	}
	return parseMessages(data)
}

func parseMessages(data []byte) ([]Message, error) {
	var msgs []Message
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, fmt.Errorf("messages: parse line: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// PathForSession returns the canonical messages.jsonl path for a session run directory.
func PathForSession(runDir string) string {
	return filepath.Join(runDir, "messages.jsonl")
}
