package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/signals"
	"github.com/hwells4/ap/internal/store"
)

func TestExpandExecHandlerArgv_AllVariables(t *testing.T) {
	payload := signalHandlerPayload{
		Type:         "escalate",
		Session:      "my-session",
		Stage:        "ralph",
		Iteration:    3,
		Reason:       "needs review",
		ChildSession: "child-1",
	}

	argv := []string{"/usr/bin/notify", "--session=${SESSION}", "--stage=${STAGE}", "--iter=${ITERATION}", "--reason=${REASON}", "--child=${CHILD_SESSION}", "--type=${TYPE}"}
	got := expandExecHandlerArgv(argv, payload)

	want := []string{"/usr/bin/notify", "--session=my-session", "--stage=ralph", "--iter=3", "--reason=needs review", "--child=child-1", "--type=escalate"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded = %v, want %v", got, want)
	}
}

func TestExpandExecHandlerArgv_CommandNameNotExpanded(t *testing.T) {
	payload := signalHandlerPayload{
		Session: "test",
		Type:    "escalate",
	}

	argv := []string{"${SESSION}-handler", "--session=${SESSION}"}
	got := expandExecHandlerArgv(argv, payload)

	// Command name (argv[0]) must NOT be expanded.
	if got[0] != "${SESSION}-handler" {
		t.Fatalf("argv[0] = %q, want ${SESSION}-handler (not expanded)", got[0])
	}
	// Arguments ARE expanded.
	if got[1] != "--session=test" {
		t.Fatalf("argv[1] = %q, want --session=test", got[1])
	}
}

func TestExpandExecHandlerArgv_EmptyFieldsExpandToEmpty(t *testing.T) {
	// When payload fields are empty, variables expand to empty string.
	payload := signalHandlerPayload{
		Type:      "escalate",
		Session:   "sess",
		Stage:     "s",
		Iteration: 1,
		// Reason, ChildSession deliberately empty
	}

	argv := []string{"cmd", "reason=${REASON}", "child=${CHILD_SESSION}"}
	got := expandExecHandlerArgv(argv, payload)

	if got[1] != "reason=" {
		t.Fatalf("argv[1] = %q, want reason= (empty expansion)", got[1])
	}
	if got[2] != "child=" {
		t.Fatalf("argv[2] = %q, want child= (empty expansion)", got[2])
	}
}

func TestExpandExecHandlerArgv_EmptyArgvReturnsEmpty(t *testing.T) {
	got := expandExecHandlerArgv(nil, signalHandlerPayload{})
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestExpandExecHandlerArgv_MultipleVarsInSingleArg(t *testing.T) {
	payload := signalHandlerPayload{
		Session:   "sess",
		Stage:     "ralph",
		Iteration: 2,
		Type:      "spawn",
	}

	argv := []string{"cmd", "${SESSION}-${STAGE}-${ITERATION}-${TYPE}"}
	got := expandExecHandlerArgv(argv, payload)

	if got[1] != "sess-ralph-2-spawn" {
		t.Fatalf("argv[1] = %q, want sess-ralph-2-spawn", got[1])
	}
}

func TestExecHandler_SuccessfulExecution(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "exec-ok", "stage", "test", "{}")

	outFile := filepath.Join(t.TempDir(), "output.txt")

	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "exec-ok",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{{
			Type: "exec",
			Argv: []string{"sh", "-c", "echo hello > " + outFile},
		}},
		Timeout: 5 * time.Second,
		Output:  io.Discard,
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Fatalf("output = %q, want hello", string(data))
	}

	// No handler errors should be emitted on success.
	evts := readEvents(t, s, "exec-ok")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 0 {
		t.Fatalf("signal.handler.error count = %d, want 0", len(handlerErrors))
	}
}

func TestExecHandler_VariableExpansionInDispatch(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "var-test", "stage", "test", "{}")

	outFile := filepath.Join(t.TempDir(), "vars.txt")

	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "var-test",
		Stage:      "analyze",
		Iteration:  7,
		SignalID:   "sig-7-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{{
			Type: "exec",
			Argv: []string{"sh", "-c", "echo ${SESSION} ${STAGE} ${ITERATION} ${TYPE} > " + outFile},
		}},
		Timeout: 5 * time.Second,
		Output:  io.Discard,
		Escalation: &signals.EscalateSignal{
			Type:   "human",
			Reason: "test",
		},
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != "var-test analyze 7 escalate" {
		t.Fatalf("output = %q, want 'var-test analyze 7 escalate'", got)
	}
}

func TestExecHandler_FailureEmitsHandlerError(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "exec-fail", "stage", "test", "{}")

	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "exec-fail",
		Stage:      "ralph",
		Iteration:  2,
		SignalID:   "sig-2-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{{
			Type: "exec",
			Argv: []string{"sh", "-c", "exit 42"},
		}},
		Timeout: 5 * time.Second,
		Output:  io.Discard,
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	evts := readEvents(t, s, "exec-fail")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	data := parseEventData(t, handlerErrors[0])
	if data["handler_type"] != "exec" {
		t.Fatalf("handler_type = %v, want exec", data["handler_type"])
	}
	errStr, _ := data["error"].(string)
	if !strings.Contains(errStr, "exec failed") {
		t.Fatalf("error = %q, want to contain 'exec failed'", errStr)
	}
}

func TestExecHandler_EmptyArgvEmitsHandlerError(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "exec-noargv", "stage", "test", "{}")

	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "exec-noargv",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{{
			Type: "exec",
			Argv: []string{},
		}},
		Timeout: 5 * time.Second,
		Output:  io.Discard,
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	evts := readEvents(t, s, "exec-noargv")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	data := parseEventData(t, handlerErrors[0])
	errStr, _ := data["error"].(string)
	if !strings.Contains(errStr, "argv is required") {
		t.Fatalf("error = %q, want to contain 'argv is required'", errStr)
	}
}

func TestExecHandler_TimeoutEmitsHandlerError(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "exec-timeout", "stage", "test", "{}")

	started := time.Now()
	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "exec-timeout",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{{
			Type: "exec",
			Argv: []string{"sleep", "60"},
		}},
		Timeout: 500 * time.Millisecond,
		Output:  io.Discard,
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	elapsed := time.Since(started)
	if elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}

	evts := readEvents(t, s, "exec-timeout")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}
	data := parseEventData(t, handlerErrors[0])
	if data["handler_type"] != "exec" {
		t.Fatalf("handler_type = %v, want exec", data["handler_type"])
	}
}

func TestExecHandler_NoRetryOnFailure(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	s.CreateSession(ctx, "exec-no-retry", "stage", "test", "{}")

	outFile := filepath.Join(t.TempDir(), "second.txt")

	err := dispatchSignalHandlers(dispatchSignalInput{
		Store:      s,
		Session:    "exec-no-retry",
		Stage:      "ralph",
		Iteration:  1,
		SignalID:   "sig-1-escalate-0",
		SignalType: "escalate",
		Handlers: []config.SignalHandler{
			{Type: "exec", Argv: []string{"sh", "-c", "exit 1"}},
			{Type: "exec", Argv: []string{"sh", "-c", "echo ok > " + outFile}},
		},
		Timeout: 5 * time.Second,
		Output:  io.Discard,
	})
	if err != nil {
		t.Fatalf("dispatchSignalHandlers() error: %v", err)
	}

	evts := readEvents(t, s, "exec-no-retry")
	handlerErrors := filterByType(evts, store.TypeSignalHandlerError)
	if len(handlerErrors) != 1 {
		t.Fatalf("signal.handler.error count = %d, want 1", len(handlerErrors))
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("second handler didn't run: %v", err)
	}
	if !strings.Contains(string(data), "ok") {
		t.Fatalf("second handler output = %q, want ok", string(data))
	}
}
