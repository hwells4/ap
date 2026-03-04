package runner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/events"
	"github.com/hwells4/ap/internal/mock"
	"github.com/hwells4/ap/internal/state"
)

func TestRun_EscalateHandlerChain_MixedSuccessAndFailure(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, ".ap", "runs", "handler-chain")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}

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
		EscalateHandlers: []config.SignalHandler{
			{Type: "exec", Argv: []string{scriptPath, "first", tracePath}},
			{Type: "webhook", URL: webhook.URL},
			{Type: "exec", Argv: []string{scriptPath, "second-${ITERATION}-${TYPE}", tracePath}},
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if res.Status != state.StatePaused {
		t.Fatalf("status = %q, want %q", res.Status, state.StatePaused)
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

	evts := readEvents(t, runDir)
	handlerErrors := filterByType(evts, events.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	if handlerErrors[0].Data["handler_type"] != "webhook" {
		t.Fatalf("handler_type = %v, want webhook", handlerErrors[0].Data["handler_type"])
	}
	if handlerErrors[0].Data["signal_id"] != "sig-1-escalate-0" {
		t.Fatalf("signal_id = %v, want sig-1-escalate-0", handlerErrors[0].Data["signal_id"])
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
