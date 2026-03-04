package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/state"
)

func runStatus(args []string, deps cliDeps) int {
	sessionName, errResp := parseStatusArgs(args)
	if errResp != nil {
		return renderError(deps, output.ExitInvalidArgs, *errResp)
	}

	projectRoot := "."
	if deps.getwd != nil {
		if cwd, err := deps.getwd(); err == nil {
			projectRoot = cwd
		}
	}

	statePath := filepath.Join(projectRoot, ".ap", "runs", sessionName, "state.json")

	if _, err := os.Stat(statePath); err != nil {
		return renderError(deps, output.ExitNotFound, output.NewError(
			"SESSION_NOT_FOUND",
			fmt.Sprintf("session %q not found", sessionName),
			fmt.Sprintf("No state.json at %s", statePath),
			"ap status <session> [--json]",
			[]string{"ap list", "ap status my-session --json"},
		))
	}

	snapshot, err := state.Load(statePath)
	if err != nil {
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"STATE_READ_FAILED",
			fmt.Sprintf("failed to read state for session %q", sessionName),
			err.Error(),
			"ap status <session> [--json]",
			nil,
		))
	}

	if deps.mode == output.ModeJSON {
		payload := output.NewSuccess(map[string]any{"snapshot": snapshot}, deps.corrections)
		serialized, err := output.MarshalSuccess(payload)
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				"failed to render status output",
				err.Error(),
				"ap status <session> [--json]",
				nil,
			))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	_, _ = fmt.Fprint(deps.stdout, renderStatusHuman(snapshot))
	return output.ExitSuccess
}

func renderStatusHuman(s *state.SessionState) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Session:    %s\n", s.Session))
	b.WriteString(fmt.Sprintf("Status:     %s\n", s.Status))
	if s.CurrentStage != "" {
		b.WriteString(fmt.Sprintf("Stage:      %s\n", s.CurrentStage))
	}
	b.WriteString(fmt.Sprintf("Iteration:  %d (completed: %d)\n", s.Iteration, s.IterationCompleted))
	b.WriteString(fmt.Sprintf("Started:    %s\n", s.StartedAt))
	if s.CompletedAt != nil {
		b.WriteString(fmt.Sprintf("Completed:  %s\n", *s.CompletedAt))
	}
	if s.Error != nil {
		b.WriteString(fmt.Sprintf("Error:      %s\n", *s.Error))
	}
	if s.ParentSession != "" {
		b.WriteString(fmt.Sprintf("Parent:     %s\n", s.ParentSession))
	}
	if len(s.ChildSessions) > 0 {
		b.WriteString(fmt.Sprintf("Children:   %s\n", strings.Join(s.ChildSessions, ", ")))
	}
	return b.String()
}

func parseStatusArgs(args []string) (string, *output.ErrorResponse) {
	var sessionName string

	for _, arg := range args {
		switch {
		case arg == "--json":
			continue
		case strings.HasPrefix(arg, "-"):
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				"ap status accepts no flags other than --json.",
				"ap status <session> [--json]",
				[]string{"ap status my-session", "ap status my-session --json"},
			)
			return "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap status takes exactly one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap status <session> [--json]",
					[]string{"ap status my-session"},
				)
				return "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	if sessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			"Provide the session name to check status.",
			"ap status <session> [--json]",
			[]string{"ap status my-session", "ap status my-session --json"},
		)
		return "", &errResp
	}

	return sessionName, nil
}
