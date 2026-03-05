package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

func runLogs(args []string, deps cliDeps) int {
	sessionName, follow, errResp := parseLogsArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	ctx := context.Background()

	// Verify session exists.
	_, err := deps.store.GetSession(ctx, sessionName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in the store.",
				"ap logs <session> [-f] [--json]",
				[]string{"ap list", "ap logs my-session --json"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to check session",
			err.Error(),
			"ap logs <session> [-f] [--json]",
			nil,
		))
	}

	if follow {
		return followLogsStore(ctx, sessionName, deps)
	}
	return dumpLogsStore(ctx, sessionName, deps)
}

func dumpLogsStore(ctx context.Context, sessionName string, deps cliDeps) int {
	events, err := deps.store.GetEvents(ctx, sessionName, "", 0)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"EVENTS_READ_FAILED",
			"failed to read events",
			err.Error(),
			"ap logs <session> [--json]",
			nil,
		))
	}

	for _, evt := range events {
		if deps.mode == output.ModeJSON {
			_, _ = fmt.Fprintln(deps.stdout, eventRowToJSON(evt))
		} else {
			_, _ = fmt.Fprintln(deps.stdout, formatEventRowHuman(evt))
		}
	}
	return output.ExitSuccess
}

func followLogsStore(ctx context.Context, sessionName string, deps cliDeps) int {
	lastSeq := 0
	for {
		events, err := deps.store.TailEvents(ctx, sessionName, lastSeq)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "error following events: %v\n", err)
			return output.ExitGeneralError
		}
		for _, evt := range events {
			if deps.mode == output.ModeJSON {
				_, _ = fmt.Fprintln(deps.stdout, eventRowToJSON(evt))
			} else {
				_, _ = fmt.Fprintln(deps.stdout, formatEventRowHuman(evt))
			}
			if evt.Seq > lastSeq {
				lastSeq = evt.Seq
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func eventRowToJSON(evt store.EventRow) string {
	obj := map[string]any{
		"seq":     evt.Seq,
		"type":    evt.Type,
		"session": evt.SessionName,
		"ts":      evt.CreatedAt,
	}
	var cursor any
	if json.Unmarshal([]byte(evt.CursorJSON), &cursor) == nil {
		obj["cursor"] = cursor
	}
	var data any
	if json.Unmarshal([]byte(evt.DataJSON), &data) == nil {
		obj["data"] = data
	}
	out, _ := json.Marshal(obj)
	return string(out)
}

func formatEventRowHuman(evt store.EventRow) string {
	ts := evt.CreatedAt
	// Compact timestamp to time only if today.
	if len(ts) > 10 {
		ts = ts[11:] // strip date prefix "YYYY-MM-DD"
		if idx := strings.Index(ts, "+"); idx > 0 {
			ts = ts[:idx]
		}
		if idx := strings.Index(ts, "Z"); idx > 0 {
			ts = ts[:idx]
		}
	}

	var b strings.Builder
	b.WriteString(ts)
	b.WriteString("  ")
	b.WriteString(evt.Type)
	if evt.SessionName != "" {
		b.WriteString("  [")
		b.WriteString(evt.SessionName)
		b.WriteString("]")
	}

	// Add cursor info if present.
	var cursor map[string]any
	if json.Unmarshal([]byte(evt.CursorJSON), &cursor) == nil {
		if iter, ok := cursor["iteration"].(float64); ok && iter > 0 {
			b.WriteString(fmt.Sprintf("  iter=%d", int(iter)))
		}
		if provider, ok := cursor["provider"].(string); ok && provider != "" {
			b.WriteString(fmt.Sprintf("  provider=%s", provider))
		}
	}

	// Add select data fields.
	var data map[string]any
	if json.Unmarshal([]byte(evt.DataJSON), &data) == nil {
		for _, key := range []string{"decision", "reason", "error", "stage"} {
			if val, ok := data[key]; ok && val != nil && val != "" {
				b.WriteString(fmt.Sprintf("  %s=%v", key, val))
			}
		}
	}

	return b.String()
}

func parseLogsArgs(args []string) (string, bool, *output.ErrorResponse) {
	var sessionName string
	var follow bool

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue
		case arg == "-f" || arg == "--follow":
			follow = true
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap logs accepts -f/--follow and --json.",
				"ap logs <session> [-f] [--json]",
				[]string{"ap logs my-session", "ap logs my-session -f --json"},
			)
			return "", false, &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap logs takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap logs <session> [-f] [--json]",
					[]string{"ap logs my-session"},
				)
				return "", false, &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to view logs.",
			"ap logs <session> [-f] [--json]",
			[]string{"ap logs my-session", "ap logs my-session -f"},
		)
		return "", false, &errResp
	}

	return sessionName, follow, nil
}
