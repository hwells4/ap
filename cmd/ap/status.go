package main

import (
	"encoding/json"
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

	// If no session specified, list all sessions with their status.
	if sessionName == "" {
		return runStatusList(projectRoot, deps)
	}

	return runStatusSession(projectRoot, sessionName, deps)
}

func runStatusSession(projectRoot, sessionName string, deps cliDeps) int {
	statePath := filepath.Join(projectRoot, ".ap", "runs", sessionName, "state.json")

	st, err := state.Load(statePath)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || os.IsNotExist(err) {
			return renderError(deps, output.ExitNotFound, output.NewError(
				"SESSION_NOT_FOUND",
				fmt.Sprintf("session %q not found", sessionName),
				fmt.Sprintf("No state.json at %s", statePath),
				"ap status <session> [--json]",
				[]string{"ap list", "ap status"},
			))
		}
		return renderError(deps, output.ExitGeneralError, output.NewError(
			"GENERAL_ERROR",
			fmt.Sprintf("failed to load session state: %v", err),
			"",
			"ap status <session> [--json]",
			nil,
		))
	}

	if deps.mode == output.ModeJSON {
		// Return state.json directly as the payload — O(1) status reads.
		payload := stateToPayload(st)
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR", "failed to render status", err.Error(),
				"ap status <session> [--json]", nil))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	// Human-readable output.
	_, _ = fmt.Fprintf(deps.stdout, "Session:    %s\n", st.Session)
	_, _ = fmt.Fprintf(deps.stdout, "Status:     %s\n", st.Status)
	_, _ = fmt.Fprintf(deps.stdout, "Iteration:  %d (completed: %d)\n", st.Iteration, st.IterationCompleted)
	if st.CurrentStage != "" {
		_, _ = fmt.Fprintf(deps.stdout, "Stage:      %s\n", st.CurrentStage)
	}
	_, _ = fmt.Fprintf(deps.stdout, "Started:    %s\n", st.StartedAt)
	if st.CompletedAt != nil {
		_, _ = fmt.Fprintf(deps.stdout, "Completed:  %s\n", *st.CompletedAt)
	}
	if st.Error != nil {
		_, _ = fmt.Fprintf(deps.stdout, "Error:      %s\n", *st.Error)
	}
	return output.ExitSuccess
}

func runStatusList(projectRoot string, deps cliDeps) int {
	runsDir := filepath.Join(projectRoot, ".ap", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			entries = nil // No sessions yet — treat as empty.
		} else {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR",
				fmt.Sprintf("failed to read runs directory: %v", err),
				"",
				"ap status [--json]",
				nil,
			))
		}
	}

	type sessionSummary struct {
		Session   string `json:"session"`
		Status    string `json:"status"`
		Iteration int    `json:"iteration"`
		StartedAt string `json:"started_at"`
	}

	var sessions []sessionSummary
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := entry.Name()
		statePath := filepath.Join(runsDir, name, "state.json")
		st, err := state.Load(statePath)
		if err != nil {
			continue // Skip sessions without valid state.
		}
		sessions = append(sessions, sessionSummary{
			Session:   st.Session,
			Status:    string(st.Status),
			Iteration: st.Iteration,
			StartedAt: st.StartedAt,
		})
	}

	if deps.mode == output.ModeJSON {
		// Ensure non-nil slice for JSON.
		if sessions == nil {
			sessions = []sessionSummary{}
		}
		payload := map[string]any{"sessions": sessions}
		serialized, err := output.MarshalSuccess(output.NewSuccess(payload, deps.corrections))
		if err != nil {
			return renderError(deps, output.ExitGeneralError, output.NewError(
				"GENERAL_ERROR", "failed to render status list", err.Error(),
				"ap status [--json]", nil))
		}
		_, _ = fmt.Fprintln(deps.stdout, string(serialized))
		return output.ExitSuccess
	}

	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(deps.stdout, "No sessions found.")
		return output.ExitSuccess
	}

	for _, s := range sessions {
		_, _ = fmt.Fprintf(deps.stdout, "%-20s %-10s iter:%d  started:%s\n",
			s.Session, s.Status, s.Iteration, s.StartedAt)
	}
	return output.ExitSuccess
}

// stateToPayload converts SessionState to a map for JSON output.
func stateToPayload(st *state.SessionState) map[string]any {
	// Marshal then unmarshal for a clean map representation.
	data, err := json.Marshal(st)
	if err != nil {
		return map[string]any{"session": st.Session, "status": string(st.Status)}
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return map[string]any{"session": st.Session, "status": string(st.Status)}
	}
	return payload
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
				"ap status [session] [--json]",
				[]string{"ap status", "ap status my-session", "ap status --json"},
			)
			return "", &errResp
		default:
			if sessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					"ap status takes at most one session name",
					fmt.Sprintf("Got %q and %q.", sessionName, arg),
					"ap status [session] [--json]",
					[]string{"ap status my-session"},
				)
				return "", &errResp
			}
			sessionName = strings.TrimSpace(arg)
		}
	}

	return sessionName, nil
}
