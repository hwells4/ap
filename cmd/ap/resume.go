package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/state"
)

func runResume(args []string, deps cliDeps) int {
	sessionName, contextOverride, errResp := parseResumeArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}

	runsDir := filepath.Join(projectRoot, ".ap", "runs")
	sessionDir := filepath.Join(runsDir, sessionName)
	statePath := filepath.Join(sessionDir, "state.json")
	requestPath := filepath.Join(sessionDir, "run_request.json")

	// Load session state.
	st, loadErr := state.Load(statePath)
	if loadErr != nil {
		return renderError(deps, output.ExitNotFound, output.NewError(
			"SESSION_NOT_FOUND",
			fmt.Sprintf("session %q not found", sessionName),
			loadErr.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap list", "ap status"},
		))
	}

	// Already running → no-op success.
	if st.Status == state.StateRunning {
		return resumeSuccess(deps, sessionName, "already_running", false, "", 0)
	}

	// Terminal states → cannot resume.
	switch st.Status {
	case state.StateCompleted:
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"INVALID_STATE",
			fmt.Sprintf("session %q is completed and cannot be resumed", sessionName),
			"Completed sessions cannot be resumed. Start a new session instead.",
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	case state.StateAborted:
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"INVALID_STATE",
			fmt.Sprintf("session %q is aborted and cannot be resumed", sessionName),
			"Aborted sessions cannot be resumed. Start a new session instead.",
			"ap resume <session> [--context TEXT] [--json]",
			[]string{"ap run <spec> <new-session>"},
		))
	}

	// Paused or failed → resume.
	// Validate that run_request.json exists and is readable.
	if _, reqErr := ReadRunRequest(requestPath); reqErr != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"CORRUPT_SESSION",
			fmt.Sprintf("cannot read run request for session %q", sessionName),
			reqErr.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	// Determine resume iteration.
	resumeIter := state.ResumeFrom(st)

	// Transition state to running.
	if _, transErr := state.Update(statePath, func(s *state.SessionState) error {
		return s.Transition(state.StateRunning)
	}); transErr != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_TRANSITION_ERROR",
			fmt.Sprintf("cannot resume session %q", sessionName),
			transErr.Error(),
			"ap resume <session> [--context TEXT] [--json]",
			nil,
		))
	}

	return resumeSuccess(deps, sessionName, "resumed", true, contextOverride, resumeIter)
}

func resumeSuccess(deps cliDeps, session, status string, wasResumed bool, contextOverride string, resumeFrom int) int {
	payload := map[string]any{
		"session":      session,
		"status":       status,
		"was_resumed":  wasResumed,
		"resume_from":  resumeFrom,
	}
	if contextOverride != "" {
		payload["context_override"] = contextOverride
	}

	if deps.mode == output.ModeJSON {
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render resume output",
				err.Error(),
				"ap resume <session> [--context TEXT] [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if wasResumed {
		_, _ = fmt.Fprintf(deps.stdout, "Resumed session %q from iteration %d.\n", session, resumeFrom)
	} else {
		_, _ = fmt.Fprintf(deps.stdout, "Session %q is already running.\n", session)
	}
	return output.ExitSuccess
}

func parseResumeArgs(args []string) (string, string, *output.ErrorResponse) {
	// Safety: NO fuzzy matching on resume — exact arguments only.
	var sessionName, contextOverride string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue // handled globally
		case arg == "--context" || arg == "-c":
			if i+1 >= len(args) {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					fmt.Sprintf("flag %q requires a value", arg),
					"Provide the context text after the flag.",
					"ap resume <session> [--context TEXT] [--json]",
					[]string{"ap resume my-session --context 'focus on tests'"},
				)
				return "", "", &errResp
			}
			i++
			contextOverride = args[i]
		case strings.HasPrefix(arg, "--context="):
			contextOverride = strings.TrimPrefix(arg, "--context=")
		case strings.HasPrefix(arg, "-c="):
			contextOverride = strings.TrimPrefix(arg, "-c=")
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap resume accepts --context and --json.",
				"ap resume <session> [--context TEXT] [--json]",
				[]string{"ap resume my-session", "ap resume my-session --context 'override'"},
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
			[]string{"ap resume my-session", "ap resume my-session --context 'override'"},
		)
		return "", "", &errResp
	}

	return sessionName, contextOverride, nil
}
