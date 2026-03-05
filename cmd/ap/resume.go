package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

func runResume(args []string, deps cliDeps) int {
	sessionName, contextOverride, errResp := parseResumeArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	ctx := context.Background()

	// Load session from store.
	row, err := deps.store.GetSession(ctx, sessionName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				"No session with that name in the store.",
				"ap resume <session> [--context TEXT] [--json]",
				[]string{"ap list", "ap status " + sessionName + " --json"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_READ_FAILED",
			fmt.Sprintf("failed to read state for session %q", sessionName),
			err.Error(),
			"ap resume <session> [--json]",
			nil,
		))
	}

	// Determine action based on current status.
	switch row.Status {
	case "running":
		return resumeAlreadyRunningStore(deps, sessionName, row)
	case "paused", "failed":
		return resumeSessionStore(deps, ctx, sessionName, row, contextOverride)
	case "completed":
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"SESSION_COMPLETED",
			fmt.Sprintf("session %q is already completed", sessionName),
			"Use 'ap run' to start a new session.",
			"ap resume <session> [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	case "aborted":
		return renderError(deps, output.ExitInvalidArgs, output.NewError(
			"SESSION_ABORTED",
			fmt.Sprintf("session %q was aborted and cannot be resumed", sessionName),
			"Use 'ap run' to start a new session.",
			"ap resume <session> [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	default:
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"INVALID_STATE",
			fmt.Sprintf("session %q has unexpected status %q", sessionName, row.Status),
			"",
			"ap resume <session> [--json]",
			[]string{"ap status " + sessionName + " --json"},
		))
	}
}

func resumeAlreadyRunningStore(deps cliDeps, session string, row *store.SessionRow) int {
	resumeFrom := row.IterationCompleted + 1
	payload := map[string]any{
		"session":     session,
		"action":      "already_running",
		"status":      row.Status,
		"iteration":   row.Iteration,
		"resume_from": resumeFrom,
	}
	return renderResumeSuccess(deps, payload)
}

func resumeSessionStore(deps cliDeps, ctx context.Context, session string, row *store.SessionRow, contextOverride string) int {
	resumeFrom := row.IterationCompleted + 1

	err := deps.store.UpdateSession(ctx, session, map[string]any{
		"status": "running",
	})
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_TRANSITION_ERROR",
			fmt.Sprintf("failed to resume session %q", session),
			err.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	payload := map[string]any{
		"session":     session,
		"action":      "resumed",
		"status":      "running",
		"resume_from": resumeFrom,
	}
	if contextOverride != "" {
		payload["context_override"] = contextOverride
	}

	// TODO: Actually re-launch via launcher with --resume flag.
	// For M0b, return the structured response indicating the session
	// would be resumed. The full launcher wiring is deferred to when
	// foreground execution lands.

	return renderResumeSuccess(deps, payload)
}

func renderResumeSuccess(deps cliDeps, payload map[string]any) int {
	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render resume output",
				err.Error(),
				"ap resume <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	action := payload["action"].(string)
	session := payload["session"].(string)
	switch action {
	case "already_running":
		_, _ = fmt.Fprintf(deps.stdout, "Session %q is already running.\n", session)
	case "resumed":
		_, _ = fmt.Fprintf(deps.stdout, "Resuming session %q from iteration %v.\n", session, payload["resume_from"])
	default:
		_, _ = fmt.Fprintf(deps.stdout, "Session %q: %s\n", session, action)
	}
	return output.ExitSuccess
}

func parseResumeArgs(args []string) (string, string, *output.ErrorResponse) {
	var sessionName, contextOverride string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--context" || arg == "-c" || strings.HasPrefix(arg, "--context=") || strings.HasPrefix(arg, "-c="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					err.Error(),
					"",
					"ap resume <session> [--context TEXT] [--json]",
					[]string{"ap resume my-session --context \"focus on tests\""},
				)
				return "", "", &errResp
			}
			i = next
			contextOverride = value
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap resume accepts --context/-c and --json.",
				"ap resume <session> [--context TEXT] [--json]",
				[]string{"ap resume my-session", "ap resume my-session --context \"new focus\" --json"},
			)
			return "", "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap resume takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap resume <session> [--context TEXT] [--json]",
					[]string{"ap resume my-session"},
				)
				return "", "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to resume.",
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap resume my-session", "ap resume my-session --json"},
		)
		return "", "", &errResp
	}

	return sessionName, contextOverride, nil
}
