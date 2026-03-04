package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/state"
)

func TestWebhookHandler_PayloadSchema(t *testing.T) {
	// Capture the payload received by the webhook endpoint.
	var capturedPayload signalHandlerPayload
	var capturedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &capturedPayload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "webhook-schema")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	prov := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need review", "human", "needs approval", []string{"approve", "reject"}),
		),
	)

	var stdout bytes.Buffer
	res, err := Run(context.Background(), Config{
		Session:              "webhook-schema",
		RunDir:               runDir,
		StageName:            "ralph",
		Provider:             prov,
		Iterations:           3,
		PromptTemplate:       "iteration ${ITERATION}",
		SignalHandlerTimeout: 5 * time.Second,
		SignalOutput:         &stdout,
		EscalateHandlers: []config.SignalHandler{
			{
				Type: "webhook",
				URL:  server.URL,
				Headers: map[string]string{
					"X-Custom-Header": "test-value",
					"Authorization":   "Bearer token123",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != state.StatePaused {
		t.Fatalf("status = %q, want paused", res.Status)
	}

	// Validate required contract fields.
	if capturedPayload.Type != "escalate" {
		t.Fatalf("type = %q, want escalate", capturedPayload.Type)
	}
	if capturedPayload.Session != "webhook-schema" {
		t.Fatalf("session = %q, want webhook-schema", capturedPayload.Session)
	}
	if capturedPayload.Iteration != 1 {
		t.Fatalf("iteration = %d, want 1", capturedPayload.Iteration)
	}
	if capturedPayload.Stage != "ralph" {
		t.Fatalf("stage = %q, want ralph", capturedPayload.Stage)
	}
	if capturedPayload.Reason != "needs approval" {
		t.Fatalf("reason = %q, want 'needs approval'", capturedPayload.Reason)
	}
	if len(capturedPayload.Options) != 2 || capturedPayload.Options[0] != "approve" {
		t.Fatalf("options = %v, want [approve, reject]", capturedPayload.Options)
	}
	if capturedPayload.Timestamp == "" {
		t.Fatal("timestamp should be non-empty")
	}

	// Validate custom headers were sent.
	if capturedHeaders.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", capturedHeaders.Get("Content-Type"))
	}
	if capturedHeaders.Get("X-Custom-Header") != "test-value" {
		t.Fatalf("X-Custom-Header = %q, want test-value", capturedHeaders.Get("X-Custom-Header"))
	}
	if capturedHeaders.Get("Authorization") != "Bearer token123" {
		t.Fatalf("Authorization = %q, want 'Bearer token123'", capturedHeaders.Get("Authorization"))
	}
}

func TestWebhookHandler_CallbackFieldsIncluded(t *testing.T) {
	var capturedPayload signalHandlerPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	ew := events.NewWriter(eventsPath)

	err := dispatchSignalHandlers(dispatchSignalInput{
		Writer:        ew,
		Session:       "callback-test",
		Stage:         "ralph",
		Iteration:     1,
		SignalID:      "sig-1-escalate-0",
		SignalType:    "escalate",
		Handlers:      []config.SignalHandler{{Type: "webhook", URL: server.URL}},
		Timeout:       5 * time.Second,
		Output:        io.Discard,
		CallbackURL:   "http://127.0.0.1:9876/callback",
		CallbackToken: "secret-token-123",
		Escalation: &signals.EscalateSignal{
			Type:    "human",
			Reason:  "review needed",
			Options: []string{"yes", "no"},
		},
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	if capturedPayload.CallbackURL != "http://127.0.0.1:9876/callback" {
		t.Fatalf("callback_url = %q, want http://127.0.0.1:9876/callback", capturedPayload.CallbackURL)
	}
	if capturedPayload.CallbackToken != "secret-token-123" {
		t.Fatalf("callback_token = %q, want secret-token-123", capturedPayload.CallbackToken)
	}
}

func TestWebhookHandler_CallbackFieldsOmittedWhenEmpty(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	ew := events.NewWriter(eventsPath)

	err := dispatchSignalHandlers(dispatchSignalInput{
		Writer:     ew,
		Session:    "no-callback",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers:   []config.SignalHandler{{Type: "webhook", URL: server.URL}},
		Timeout:    5 * time.Second,
		Output:     io.Discard,
		Escalation: &signals.EscalateSignal{
			Type:    "human",
			Reason:  "test",
			Options: []string{},
		},
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	// callback_url and callback_token should not appear in the JSON body.
	bodyStr := string(capturedBody)
	if strings.Contains(bodyStr, "callback_url") {
		t.Fatalf("callback_url should be omitted when empty, got: %s", bodyStr)
	}
	if strings.Contains(bodyStr, "callback_token") {
		t.Fatalf("callback_token should be omitted when empty, got: %s", bodyStr)
	}
}

func TestWebhookHandler_HTTPErrorReportsHandlerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	ew := events.NewWriter(eventsPath)

	err := dispatchSignalHandlers(dispatchSignalInput{
		Writer:     ew,
		Session:    "http-error",
		Stage:      "ralph",
		Iteration:  2,
		SignalID:   "sig-2-escalate-0",
		SignalType: "escalate",
		Handlers:   []config.SignalHandler{{Type: "webhook", URL: server.URL}},
		Timeout:    5 * time.Second,
		Output:     io.Discard,
		Escalation: &signals.EscalateSignal{
			Type:   "human",
			Reason: "test",
		},
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	// Verify signal.handler.error event was emitted.
	evts := readEventsFromPath(t, eventsPath)
	handlerErrors := filterByType(evts, events.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	if handlerErrors[0].Data["handler_type"] != "webhook" {
		t.Fatalf("handler_type = %v, want webhook", handlerErrors[0].Data["handler_type"])
	}
	errStr, _ := handlerErrors[0].Data["error"].(string)
	if !strings.Contains(errStr, "503") {
		t.Fatalf("error should contain status 503, got %q", errStr)
	}
}

func TestWebhookHandler_TimeoutReportsHandlerError(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-done
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(done)

	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	ew := events.NewWriter(eventsPath)

	started := time.Now()
	err := dispatchSignalHandlers(dispatchSignalInput{
		Writer:     ew,
		Session:    "timeout-test",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers:   []config.SignalHandler{{Type: "webhook", URL: server.URL}},
		Timeout:    500 * time.Millisecond,
		Output:     io.Discard,
		Escalation: &signals.EscalateSignal{
			Type:   "human",
			Reason: "test",
		},
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	elapsed := time.Since(started)
	if elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}

	evts := readEventsFromPath(t, eventsPath)
	handlerErrors := filterByType(evts, events.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
}

func TestWebhookHandler_SpawnPayloadIncludesChildSession(t *testing.T) {
	var capturedPayload signalHandlerPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	ew := events.NewWriter(eventsPath)

	err := dispatchSignalHandlers(dispatchSignalInput{
		Writer:       ew,
		Session:      "parent-session",
		Stage:        "ralph",
		Iteration:    1,
		SignalID:     "sig-1-spawn-0",
		SignalType:   "spawn",
		Handlers:     []config.SignalHandler{{Type: "webhook", URL: server.URL}},
		Timeout:      5 * time.Second,
		Output:       io.Discard,
		ChildSession: "child-session-1",
		ChildStage:   "refine:5",
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	if capturedPayload.Type != "spawn" {
		t.Fatalf("type = %q, want spawn", capturedPayload.Type)
	}
	if capturedPayload.ChildSession != "child-session-1" {
		t.Fatalf("child_session = %q, want child-session-1", capturedPayload.ChildSession)
	}
	if capturedPayload.ChildStage != "refine:5" {
		t.Fatalf("child_stage = %q, want refine:5", capturedPayload.ChildStage)
	}
}

// readEventsFromPath reads events from a specific events.jsonl path.
func readEventsFromPath(t *testing.T, path string) []events.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var evts []events.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev events.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		evts = append(evts, ev)
	}
	return evts
}
