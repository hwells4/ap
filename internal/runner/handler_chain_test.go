package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/store"
)

func TestRun_EscalateHandlerChain_MixedSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "handler-chain")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	s := mustOpenStore(t)

	tracePath := filepath.Join(root, "handler-order.log")
	scriptPath := filepath.Join(root, "record-handler.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$2\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// Force one handler failure in the middle of the chain.
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer webhook.Close()

	prov := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need review", "human", "pick one", []string{"A", "B"}),
		),
	)

	var stdout bytes.Buffer
	res, err := Run(context.Background(), Config{
		Session:              "handler-chain",
		RunDir:               runDir,
		StageName:            "ralph",
		Provider:             prov,
		Iterations:           3,
		PromptTemplate:       "iteration ${ITERATION}",
		SignalHandlerTimeout: 2 * time.Second,
		SignalOutput:         &stdout,
		Store:                s,
		EscalateHandlers: []config.SignalHandler{
			{Type: "exec", Argv: []string{scriptPath, "first", tracePath}},
			{Type: "webhook", URL: webhook.URL},
			{Type: "exec", Argv: []string{scriptPath, "second-${ITERATION}-${TYPE}", tracePath}},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}
	if res.Iterations != 1 {
		t.Fatalf("iterations = %d, want 1", res.Iterations)
	}

	orderBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read handler trace: %v", err)
	}
	gotOrder := strings.Split(strings.TrimSpace(string(orderBytes)), "\n")
	wantOrder := []string{"first", "second-1-escalate"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("handler order = %#v, want %#v", gotOrder, wantOrder)
	}

	evts := readEvents(t, s, "handler-chain")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	errData := parseEventData(t, handlerErrors[0])
	if errData["handler_type"] != "webhook" {
		t.Fatalf("handler_type = %v, want webhook", errData["handler_type"])
	}
	if errData["signal_id"] != "sig-1-escalate-0" {
		t.Fatalf("signal_id = %v, want sig-1-escalate-0", errData["signal_id"])
	}

	// stdout fallback is always appended even when not explicitly configured.
	if !strings.Contains(stdout.String(), `"type":"escalate"`) {
		t.Fatalf("stdout fallback output missing escalate payload: %q", stdout.String())
	}
}

func TestHandlersWithStdoutFallback_AlwaysTerminalStdout(t *testing.T) {
	handlers := []config.SignalHandler{
		{Type: "stdout"},
		{Type: "webhook", URL: "https://example.com/hook"},
		{Type: "stdout"},
		{Type: "exec", Argv: []string{"echo", "hi"}},
	}

	chain := handlersWithStdoutFallback(handlers)
	if len(chain) != 3 {
		t.Fatalf("len(chain) = %d, want 3", len(chain))
	}
	if chain[0].Type != "webhook" {
		t.Fatalf("chain[0].Type = %q, want webhook", chain[0].Type)
	}
	if chain[1].Type != "exec" {
		t.Fatalf("chain[1].Type = %q, want exec", chain[1].Type)
	}
	if chain[2].Type != "stdout" {
		t.Fatalf("chain[2].Type = %q, want stdout", chain[2].Type)
	}
}

func TestRun_WebhookEscalatePayloadContract(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "webhook-escalate")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	s := mustOpenStore(t)

	type webhookRequest struct {
		Method  string
		Headers http.Header
		Payload map[string]any
	}
	requests := make(chan webhookRequest, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- webhookRequest{
			Method:  r.Method,
			Headers: r.Header.Clone(),
			Payload: payload,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	prov := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need review", "human", "pick one", []string{"A", "B"}),
		),
	)

	var stdout bytes.Buffer
	res, err := Run(context.Background(), Config{
		Session:              "webhook-escalate",
		RunDir:               runDir,
		StageName:            "ralph",
		Provider:             prov,
		Iterations:           3,
		PromptTemplate:       "iteration ${ITERATION}",
		SignalHandlerTimeout: 2 * time.Second,
		SignalOutput:         &stdout,
		Store:                s,
		EscalateHandlers: []config.SignalHandler{
			{Type: "webhook", URL: webhook.URL, Headers: map[string]string{"X-Test-Header": "ok"}},
		},
		CallbackURL:   "http://127.0.0.1:41823/resume",
		CallbackToken: "test-token",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}

	var req webhookRequest
	select {
	case req = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook request")
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got := req.Headers.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	if got := req.Headers.Get("X-Test-Header"); got != "ok" {
		t.Fatalf("X-Test-Header = %q, want ok", got)
	}

	payload := req.Payload
	if payload["type"] != "escalate" {
		t.Fatalf("payload.type = %v, want escalate", payload["type"])
	}
	if payload["session"] != "webhook-escalate" {
		t.Fatalf("payload.session = %v, want webhook-escalate", payload["session"])
	}
	if payload["stage"] != "ralph" {
		t.Fatalf("payload.stage = %v, want ralph", payload["stage"])
	}
	if payload["iteration"] != float64(1) {
		t.Fatalf("payload.iteration = %v, want 1", payload["iteration"])
	}
	if payload["reason"] != "pick one" {
		t.Fatalf("payload.reason = %v, want pick one", payload["reason"])
	}
	options, ok := payload["options"].([]any)
	if !ok || len(options) != 2 {
		t.Fatalf("payload.options = %#v, want 2 options", payload["options"])
	}
	if payload["callback_url"] != "http://127.0.0.1:41823/resume" {
		t.Fatalf("payload.callback_url = %v, want callback URL", payload["callback_url"])
	}
	if payload["callback_token"] != "test-token" {
		t.Fatalf("payload.callback_token = %v, want test-token", payload["callback_token"])
	}
	timestamp, _ := payload["timestamp"].(string)
	if timestamp == "" {
		t.Fatal("payload.timestamp is empty")
	}
	if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
		t.Fatalf("payload.timestamp parse error: %v (value=%q)", err, timestamp)
	}
}

func TestRun_WebhookSpawnPayloadContract(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "webhook-spawn")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	s := mustOpenStore(t)

	type webhookRequest struct {
		Payload map[string]any
	}
	requests := make(chan webhookRequest, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- webhookRequest{Payload: payload}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	prov := mock.New(
		mock.WithResponses(mock.Response{
			Decision: "stop",
			Reason:   "done",
			Summary:  "spawn child",
			Signals: &mock.Signals{
				Spawn: json.RawMessage(`[{"run":"ralph","session":"child-hook"}]`),
			},
		}),
	)

	launcher := &spawnTestLauncher{}
	res, err := Run(context.Background(), Config{
		Session:              "webhook-spawn",
		RunDir:               runDir,
		StageName:            "ralph",
		Provider:             prov,
		Iterations:           1,
		PromptTemplate:       "iteration ${ITERATION}",
		WorkDir:              root,
		Launcher:             launcher,
		SignalHandlerTimeout: 2 * time.Second,
		Store:                s,
		SpawnHandlers: []config.SignalHandler{
			{Type: "webhook", URL: webhook.URL},
		},
		CallbackURL:   "http://127.0.0.1:41823/resume",
		CallbackToken: "spawn-token",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusCompleted {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusCompleted)
	}

	var req webhookRequest
	select {
	case req = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for spawn webhook request")
	}

	payload := req.Payload
	if payload["type"] != "spawn" {
		t.Fatalf("payload.type = %v, want spawn", payload["type"])
	}
	if payload["session"] != "webhook-spawn" {
		t.Fatalf("payload.session = %v, want webhook-spawn", payload["session"])
	}
	if payload["stage"] != "ralph" {
		t.Fatalf("payload.stage = %v, want ralph", payload["stage"])
	}
	if payload["iteration"] != float64(1) {
		t.Fatalf("payload.iteration = %v, want 1", payload["iteration"])
	}
	if payload["child_session"] != "child-hook" {
		t.Fatalf("payload.child_session = %v, want child-hook", payload["child_session"])
	}
	if payload["child_stage"] != "ralph" {
		t.Fatalf("payload.child_stage = %v, want ralph", payload["child_stage"])
	}
	if payload["callback_url"] != "http://127.0.0.1:41823/resume" {
		t.Fatalf("payload.callback_url = %v, want callback URL", payload["callback_url"])
	}
	if payload["callback_token"] != "spawn-token" {
		t.Fatalf("payload.callback_token = %v, want spawn-token", payload["callback_token"])
	}
}

func TestRun_WebhookTimeoutEmitsHandlerError(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "webhook-timeout")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

	s := mustOpenStore(t)

	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	prov := mock.New(
		mock.WithResponses(
			mock.EscalateResponse("continue", "need review", "human", "pick one", []string{"A", "B"}),
		),
	)

	res, err := Run(context.Background(), Config{
		Session:              "webhook-timeout",
		RunDir:               runDir,
		StageName:            "ralph",
		Provider:             prov,
		Iterations:           3,
		PromptTemplate:       "iteration ${ITERATION}",
		SignalHandlerTimeout: 25 * time.Millisecond,
		Store:                s,
		EscalateHandlers: []config.SignalHandler{
			{Type: "webhook", URL: webhook.URL},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != store.StatusPaused {
		t.Fatalf("status = %q, want %q", res.Status, store.StatusPaused)
	}

	evts := readEvents(t, s, "webhook-timeout")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	errData := parseEventData(t, handlerErrors[0])
	if errData["handler_type"] != "webhook" {
		t.Fatalf("handler_type = %v, want webhook", errData["handler_type"])
	}
	errMsg, _ := errData["error"].(string)
	msg := strings.ToLower(errMsg)
	if !strings.Contains(msg, "timeout") && !strings.Contains(msg, "deadline exceeded") {
		t.Fatalf("handler error = %q, want timeout/deadline exceeded", errMsg)
	}
}
