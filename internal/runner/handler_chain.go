package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/config"
	"github.com/hwells4/ap/internal/events"
	apexec "github.com/hwells4/ap/internal/exec"
	"github.com/hwells4/ap/internal/signals"
)

const defaultSignalHandlerTimeout = 30 * time.Second

type dispatchSignalInput struct {
	Writer        *events.Writer
	Session       string
	Stage         string
	Iteration     int
	SignalID      string
	SignalType    string
	Handlers      []config.SignalHandler
	Timeout       time.Duration
	Output        io.Writer
	Escalation    *signals.EscalateSignal
	ChildSession  string
	ChildStage    string
	CallbackURL   string
	CallbackToken string
}

type signalHandlerPayload struct {
	Type          string   `json:"type"`
	Session       string   `json:"session"`
	Iteration     int      `json:"iteration"`
	Stage         string   `json:"stage"`
	Reason        string   `json:"reason,omitempty"`
	Options       []string `json:"options,omitempty"`
	ChildSession  string   `json:"child_session,omitempty"`
	ChildStage    string   `json:"child_stage,omitempty"`
	CallbackURL   string   `json:"callback_url,omitempty"`
	CallbackToken string   `json:"callback_token,omitempty"`
	Timestamp     string   `json:"timestamp"`
}

func dispatchSignalHandlers(input dispatchSignalInput) error {
	if input.Writer == nil {
		return fmt.Errorf("dispatch handlers: writer is required")
	}

	timeout := input.Timeout
	if timeout <= 0 {
		timeout = defaultSignalHandlerTimeout
	}

	payload := signalHandlerPayload{
		Type:          strings.TrimSpace(input.SignalType),
		Session:       strings.TrimSpace(input.Session),
		Iteration:     input.Iteration,
		Stage:         strings.TrimSpace(input.Stage),
		ChildSession:  strings.TrimSpace(input.ChildSession),
		ChildStage:    strings.TrimSpace(input.ChildStage),
		CallbackURL:   strings.TrimSpace(input.CallbackURL),
		CallbackToken: strings.TrimSpace(input.CallbackToken),
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	if input.Escalation != nil {
		payload.Reason = strings.TrimSpace(input.Escalation.Reason)
		payload.Options = append([]string(nil), input.Escalation.Options...)
	}

	handlers := handlersWithStdoutFallback(input.Handlers)
	for idx, handler := range handlers {
		handlerType := strings.ToLower(strings.TrimSpace(handler.Type))
		var runErr error

		switch handlerType {
		case "stdout":
			runErr = runStdoutHandler(input.Output, payload)
		case "webhook":
			runErr = runWebhookHandler(timeout, handler, payload)
		case "exec":
			runErr = runExecHandler(timeout, handler, payload)
		default:
			runErr = fmt.Errorf("unsupported handler type %q", handler.Type)
		}

		if runErr == nil {
			continue
		}

		if err := input.Writer.Append(events.NewEvent(events.TypeSignalHandlerError, input.Session, &events.Cursor{
			Iteration: input.Iteration,
		}, map[string]any{
			"iteration":     input.Iteration,
			"signal_id":     input.SignalID,
			"signal_type":   input.SignalType,
			"handler_type":  handlerType,
			"handler_index": idx,
			"error":         runErr.Error(),
		})); err != nil {
			return fmt.Errorf("emit signal.handler.error: %w", err)
		}
	}

	return nil
}

func handlersWithStdoutFallback(handlers []config.SignalHandler) []config.SignalHandler {
	// Preserve configured order for non-stdout handlers, and always keep a
	// single stdout handler as the terminal backstop.
	chain := make([]config.SignalHandler, 0, len(handlers)+1)
	for _, handler := range handlers {
		if strings.EqualFold(strings.TrimSpace(handler.Type), "stdout") {
			continue
		}
		chain = append(chain, handler)
	}
	chain = append(chain, config.SignalHandler{Type: "stdout"})
	return chain
}

func runStdoutHandler(out io.Writer, payload signalHandlerPayload) error {
	if out == nil {
		out = os.Stdout
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal stdout payload: %w", err)
	}
	if _, err := fmt.Fprintln(out, string(encoded)); err != nil {
		return fmt.Errorf("write stdout payload: %w", err)
	}
	return nil
}

func runWebhookHandler(timeout time.Duration, handler config.SignalHandler, payload signalHandlerPayload) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(handler.URL), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range handler.Headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

func runExecHandler(timeout time.Duration, handler config.SignalHandler, payload signalHandlerPayload) error {
	if len(handler.Argv) == 0 {
		return fmt.Errorf("exec argv is required")
	}
	argv := expandExecHandlerArgv(handler.Argv, payload)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := apexec.Command(ctx, argv[0], argv[1:]...)
	opts := apexec.DefaultOptions()
	opts.MinTime = 0
	opts.GracePeriod = 2 * time.Second

	res, err := apexec.Run(ctx, cmd, opts)
	if err == nil {
		return nil
	}

	if res != nil {
		stderr := strings.TrimSpace(string(res.Stderr))
		if stderr != "" {
			return fmt.Errorf("exec failed: %w: %s", err, stderr)
		}
	}
	return fmt.Errorf("exec failed: %w", err)
}

func expandExecHandlerArgv(argv []string, payload signalHandlerPayload) []string {
	if len(argv) == 0 {
		return []string{}
	}

	expanded := make([]string, len(argv))
	expanded[0] = argv[0] // command name is not expanded
	replacer := strings.NewReplacer(
		"${SESSION}", payload.Session,
		"${STAGE}", payload.Stage,
		"${ITERATION}", strconv.Itoa(payload.Iteration),
		"${REASON}", payload.Reason,
		"${CHILD_SESSION}", payload.ChildSession,
		"${TYPE}", payload.Type,
	)
	for i := 1; i < len(argv); i++ {
		expanded[i] = replacer.Replace(argv[i])
	}
	return expanded
}
